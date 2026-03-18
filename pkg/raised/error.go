package raised

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

const (
	traceSize    = 8
	maxCached    = 32
	filelineFmt  = "\n  file: %s line: %d\n"
	missFileline = "\n  file: ? line: ?\n"
)

// Key is a hashcode derived from an Error's propagation path and terminal
// error identities. Two Error values sharing the same Key are considered
// to represent the same problem: identical code path and equivalent root cause.
type Key = uint64

// Error extends the standard error interface with propagation tracing and
// structural comparison. Instances are created exclusively via Trace.
type Error interface {
	error

	// Cause returns the root error that initiated this trace,
	// typically a SentinelError or a well-typed external error.
	Cause() error

	// Trace returns a formatted traceback string listing each Trace call
	// site in order from most recent to oldest, including file and line
	// information. Use %+v with fmt to obtain the same output.
	Trace() string

	// Classify overrides the sentinel identity of this error from the caller's
	// perspective. It is intended for use by packages that receive a foreign error
	// and want to assert their own interpretation of what that error means,
	// without changing the underlying cause.
	//
	// Example:
	//
	//	err = raised.Trace(err, flk, "storage unavailable")
	//	err.Classify(ErrServiceUnavailable)
	Classify(SentinelError)
}

// errTrace is the private implementation of the Error interface.
// It records the propagation path of an error as a sequence of (PC, message)
// pairs accumulated by successive Trace calls. Memory use is bounded: up to
// traceSize steps are stored in fixed-size arrays, with older middle entries
// compressed out when the limit is exceeded, preserving the first and most
// recent call sites.
//
// Concurrency: errTrace is safe for concurrent use. A single mutex guards
// all fields including the lazily computed _summary and _trace strings.
// Contention is expected to be low since errors are typically created on
// one goroutine and read on another after being passed over a channel.
type errTrace struct {
	// mut contention expected to be low but errTrace may be transmitted over channel.
	mut sync.Mutex

	// the root error, set once at construction and never mutated.
	cause error

	// TODO: doc
	class SentinelError

	// index of the next free slot in pcs/msgs.
	// also encodes total Trace call count including compressed-out entries.
	next int

	// program counter for each Trace call site.
	pcs [traceSize]uintptr

	// message string supplied at each Trace call site, parallel to pcs.
	msgs [traceSize]string

	// ---
	// fields below are transient

	// guards lazy initialisation of _summary and _trace; cleared whenever pcs or msgs are mutated.
	_loaded  bool
	_summary string
	_trace   string
}

// Trace records a propagation step for err, attaching msg and the call site PC
// to the trace. If err is already a traced error it is extended in place;
// otherwise a new trace is created with err as its cause.
// If args are provided, msg is used as a format string.
// Returns nil if err is nil.
func Trace(err error, msg string, args ...any) error {
	if nil == err {
		return nil
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	rv, ok := err.(*errTrace)
	if !ok {
		rv = &errTrace{cause: err}
	} else {
		// rv not created here, hence we lock
		rv.mut.Lock()
		defer rv.mut.Unlock()
	}
	addCallerInfo(rv, 0, msg, 1)
	return rv
}

// TraceAt behaves like Trace but caches the call site PC using flk as a
// lookup key, avoiding repeated runtime.Callers calls on hot paths.
// flk must be a non-zero integer constant unique within the calling package.
// If args are provided, msg is used as a format string.
// Returns nil if err is nil.
func TraceAt[K ~int](flk K, err error, msg string, args ...any) error {
	if nil == err {
		return nil
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	rv, ok := err.(*errTrace)
	if !ok {
		rv = &errTrace{cause: err}
	} else {
		// rv not created here, hence we lock
		rv.mut.Lock()
		defer rv.mut.Unlock()
	}
	addCallerInfo(rv, flk, msg, 1)
	return rv
}

func (self *errTrace) Error() string {
	self.mut.Lock()
	defer self.mut.Unlock()
	if !self._loaded {
		self.loadInfo()
	}
	return self._summary
}

func (self *errTrace) Cause() error {
	self.mut.Lock()
	defer self.mut.Unlock()
	return self.cause
}

func (self *errTrace) Trace() string {
	self.mut.Lock()
	defer self.mut.Unlock()
	if !self._loaded {
		self.loadInfo()
	}
	return self._trace
}

func (self *errTrace) Format(f fmt.State, verb rune) {
	self.mut.Lock()
	defer self.mut.Unlock()
	if !self._loaded {
		self.loadInfo()
	}
	switch verb {
	case 'v':
		if f.Flag('+') {
			io.WriteString(f, self._trace)
		} else {
			io.WriteString(f, self._summary)
		}
	case 's':
		io.WriteString(f, self._summary)
	}
}

func (self *errTrace) Unwrap() []error {
	self.mut.Lock()
	defer self.mut.Unlock()
	if nil != self.class {
		return []error{self.class, self.cause}
	}

	return []error{self.cause}
}

func (self *errTrace) Classify(err SentinelError) {
	self.mut.Lock()
	defer self.mut.Unlock()
	self.classify(err)
}

// causeString returns the Error() string of self.cause.
func (self *errTrace) causeString() string {
	if nil != self.cause {
		return self.cause.Error()
	} else {
		return ""
	}
}

func (self *errTrace) classify(err SentinelError) {
	self.class = err
}

// genInfo renders the summary and full trace strings for self into dst.
// When next exceeds traceSize, middle entries are represented as an omission
// count, preserving the first and most recent call sites in the output.
func (self *errTrace) genInfo(dst *errInfo) {
	cause := self.causeString()
	if 0 == self.next {
		dst.summary = cause
		dst.trace = cause
		return
	}

	start := self.next - 1
	maxstart := traceSize - 1
	misscount := 0
	misspos := -1
	if start > maxstart {
		misscount = start - maxstart
		misspos = traceSize / 2
		start = maxstart
	}

	// render full error trace
	// note that the trace is "compressed" if it has more than traceSize turns
	var flstart, omitfmt string
	var tb strings.Builder
	tb.WriteString("Traceback [most recent call first]:\n")
	fls := getFileLines(self.pcs[:1+start])
	msgs := self.msgs
	for i := start; i >= 0; i -= 1 {

		// write trace msg
		tb.WriteString(msgs[i])

		// write trace file/line
		tb.WriteString(fls[i])

		// save flstart which gives file/line for trace summary
		if i == start {
			flstart = fls[i]
		}

		// insert misscount if it is > 0
		if i == misspos {
			if 1 == misscount {
				omitfmt = "    [%d omission...]\n"
			} else {
				omitfmt = "    [%d omissions...]\n"
			}
			tb.WriteString(fmt.Sprintf(omitfmt, misscount))
		}
	}
	if cause != msgs[0] {
		tb.WriteString("Caused by:\n  ")
		tb.WriteString(cause)
	}
	dst.trace = tb.String()

	// generate the summary
	tb.Reset()
	tb.WriteString(msgs[start])
	tb.WriteString(flstart)
	dst.summary = tb.String()

}

// getFileLines resolves a slice of PCs to formatted file/line strings.
func getFileLines(pcs []uintptr) []string {
	if 0 == len(pcs) {
		return nil
	}

	fls := make([]string, 0, len(pcs))

	frames := runtime.CallersFrames(pcs)
	var frame runtime.Frame
	var more bool
	var filename, fileline, funcname, pkgpath string
	var dotpos int
	for {
		frame, more = frames.Next()

		// extract base filename
		filename = filepath.Base(frame.File)

		// funcname in canonical form module/package.funcname[.component]
		// funcname may have more than 1 component if func is instantiated dynamically by a factory
		funcname = frame.Function

		// extract pkgpath in canonical form module/package
		dotpos = strings.LastIndex(funcname, "/")
		if dotpos < 0 {
			dotpos = 0
		}
		dotpos += strings.Index(funcname[dotpos:], ".")
		if dotpos >= 0 {
			pkgpath = funcname[:dotpos]

			// filename is in canonical form module/package/filename
			// module is relative to current buildModPath
			filename, _ = strings.CutPrefix(path.Join(pkgpath, filename), buildModPath)

			fileline = fmt.Sprintf(filelineFmt, filename, frame.Line)

		} else {

			fileline = missFileline
		}

		fls = append(fls, fileline)

		if !more {
			break
		}
	}

	return fls
}

// buildModPath is the module path prefix stripped from file names in traces.
var buildModPath string

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		buildModPath = info.Main.Path + "/"
	}
}

// addCallerInfo appends a (PC, msg) pair to err's propagation trace.
// When the trace is full, middle entries are compressed out, preserving
// the first and most recent call sites. skip adjusts the call stack depth
// passed to getPC so that the recorded PC points to the caller of Trace
// or TraceAt, not to addCallerInfo itself.
// Must be called with err.mut held when err was not freshly allocated.
func addCallerInfo[K ~int](err *errTrace, flk K, msg string, skip int) {

	// pc acquisition same as log/slog
	// var pc uintptr
	// var pcs [1]uintptr
	// runtime.Callers(2+skip, pcs[:])
	// pc = pcs[0]
	pc := getPC(pckey[K]{flk, 3 + skip})

	pos := err.next
	if pos >= traceSize {
		// we "compress" the error path by keeping only begin & end
		copy(err.pcs[4:], err.pcs[5:])
		copy(err.msgs[4:], err.msgs[5:])
		pos = traceSize - 1
	}
	err.pcs[pos] = pc
	err.msgs[pos] = strings.TrimSpace(msg)

	// clear cached summary & trace
	err._loaded = false
	err._summary = ""
	err._trace = ""

	err.next += 1
}

// ---
// program counter resolution caching

var pcCache sync.Map

// pckey is the cache key for getPC.
// flk identifies the call site; skip is the runtime.Callers depth offset.
// Two pckey values with the same flk and skip always resolve to the same PC.
type pckey[K ~int] struct {
	flk  K // fileline key
	skip int
}

// getPC returns the program counter for the call site identified by ck.
// If ck.flk is zero, caching is skipped and runtime.Callers is called directly.
// Otherwise the result is stored in pcCache and reused on subsequent calls,
// making PC resolution effectively free after the first call per site.
func getPC[K ~int](ck pckey[K]) uintptr {
	if 0 == ck.flk {
		// we don't access the cache if the fileline key is 0
		var pcs [1]uintptr
		runtime.Callers(ck.skip, pcs[:])
		return pcs[0]
	}

	var pc uintptr
	var ok bool
	val, found := pcCache.Load(ck)
	if found {
		pc, ok = val.(uintptr)
		if !ok {
			pc = 0
			pcCache.CompareAndDelete(ck, val)
		}
	}
	if 0 == pc {
		var pcs [1]uintptr
		runtime.Callers(ck.skip, pcs[:])
		val, _ = pcCache.LoadOrStore(ck, pcs[0])
		pc = val.(uintptr) // if val was again invalid this would indicate a big issue, better panic in this case
	}

	return pc
}

// ---
// Work In Progress
// errTrace _trace & _summary caching
// allows saving allocations if Trace msg are constants
// need to improve L2 cache for it not to drain resources when caching is not possible

var errCache sync.Map

// errInfo holds the rendered string forms of an errTrace.
// Both fields are derived entirely from errTrace content and are safe to cache.
type errInfo struct {
	summary string
	trace   string
}

// cacheKey is the outer key for errCache, derived from the PC trace alone.
// Two errTrace values with identical pcs and turnCount took the same code path
// and share a cacheSlot.
type cacheKey struct {
	pcs       [traceSize]uintptr
	turnCount int
}

// cacheL2Key is the inner key within a cacheSlot, derived from the cause
// string and message array. It discriminates between errTrace values that
// share a code path but differ in cause or messages.
type cacheL2Key struct {
	cause string
	msgs  [traceSize]string
}

// cacheSlot holds the L2 cache for a single code path (cacheKey).
// Multiple (cause, msgs) combinations may map to distinct errInfo values
// within the same slot.
type cacheSlot struct {
	mut   sync.RWMutex
	infos map[cacheL2Key]errInfo
	/*
		TODO:
		consider replacing infos map with ring buffer using below fields.
		cache eviction is a snap in this case.
		drawback is number of keys comparison.
		if count > 1, caching unlikely to work hence maxCached can be reduced.

		keys  [maxCached]cacheL2Key
		infos [maxCached]errInfo
		count int // number of valid entries, capped at maxCached
		next  int // ring buffer insertion point
	*/
}

// newCacheSlot allocates an empty cacheSlot.
func newCacheSlot() *cacheSlot {
	return &cacheSlot{infos: make(map[cacheL2Key]errInfo)}
}

// loadInfo computes and caches _summary and _trace for self.
// It performs a two-level cache lookup: first by code path (pcs, next),
// then by content (cause, msgs). On a miss at the second level it calls
// genInfo to render the strings and stores the result.
// Must be called with self.mut held.
func (self *errTrace) loadInfo() {
	var slot *cacheSlot
	var ok bool
	ck := cacheKey{turnCount: self.next, pcs: self.pcs}
	val, found := errCache.Load(ck)
	if found {
		slot, ok = val.(*cacheSlot)
		if !ok {
			slot = nil
			errCache.CompareAndDelete(ck, val) // val should not be in map
		}
	}
	if nil == slot {
		val, _ = errCache.LoadOrStore(ck, newCacheSlot())
		slot = val.(*cacheSlot) // if val was again invalid this would indicate a big issue, better panic in this case...
	}

	// lock retrieved slot for reading
	slot.mut.RLock()
	readLocked := true
	releaseRLock := func() {
		if readLocked {
			slot.mut.RUnlock()
			readLocked = false
		}
	}
	defer releaseRLock()

	var loaded *errInfo
	defer func() {
		// update local cache
		if nil != loaded {
			self._loaded = true
			self._summary = loaded.summary
			self._trace = loaded.trace
		}
	}()

	sk := cacheL2Key{cause: self.causeString(), msgs: self.msgs}
	info, found := slot.infos[sk]
	if found {
		loaded = &info // saved by exit defer
		return
	}

	// lock retrieved slot for writing
	releaseRLock()
	slot.mut.Lock()
	defer slot.mut.Unlock()

	// recheck the cache in case it was updated meanwhile locking
	info, found = slot.infos[sk]
	if found {
		loaded = &info // saved by exit defer
		return
	}

	self.genInfo(&info)
	if len(slot.infos) > maxCached {
		// TODO: improve this
		slot.infos = make(map[cacheL2Key]errInfo, maxCached)
	}
	slot.infos[sk] = info

	// exit defer save loaded in local cache
	loaded = &info

}
