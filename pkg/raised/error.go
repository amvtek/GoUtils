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
	"time"
)

// Sizing constants for errTrace.
// traceSize is the maximum number of (PC, message) pairs retained in a trace;
// middle entries are compressed out beyond this limit.
// filelineFmt and missFileline are the formatted file/line templates used
// in trace output; missFileline is substituted when symbol resolution fails.
const (
	traceSize    = 8
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
	// perspective. Intended for packages that receive a foreign error and want
	// to assert their own interpretation without changing the underlying cause.
	//
	// Example:
	//
	//	err = raised.Trace(err, "storage unavailable")
	//	err.Classify(ErrServiceUnavailable)
	Classify(SentinelError)
}

// errTrace is the private implementation of the Error interface.
// It records the propagation path as a fixed-size sequence of (PC, message)
// pairs; middle entries are compressed out when traceSize is exceeded,
// preserving the first and most recent call sites.
//
// _summary and _trace are lazily populated on first use and invalidated
// whenever a new propagation step is added. They are write-through caches
// backed by the package-level summaryCache and traceCache.
//
// Concurrency: all fields are guarded by mut. Contention is expected to be
// low since errors are typically written on one goroutine and read on another.
type errTrace struct {
	// mut guards all fields. Contention is expected to be low but errTrace
	// may be transmitted over a channel between goroutines.
	mut sync.Mutex

	// cause is the root error, set once at construction and never mutated.
	cause error

	// class is the sentinel assigned via Classify, overriding the error's
	// identity for errors.Is matching without changing cause.
	class SentinelError

	// next is the index of the next free slot in pcs/msgs, and encodes the
	// total Trace call count including compressed-out entries.
	next int

	// pcs holds the program counter for each recorded Trace call site.
	pcs [traceSize]uintptr

	// msgs holds the message string supplied at each Trace call site, parallel to pcs.
	msgs [traceSize]string

	// _summary and _trace are lazily computed and cached renderings.
	// Both are invalidated (set to "") whenever a new propagation step is added.
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

// Error returns a short summary of the most recent propagation step,
// consisting of the step message and its file/line location.
// The result is cached in _summary and backed by summaryCache.
func (self *errTrace) Error() string {
	self.mut.Lock()
	defer self.mut.Unlock()

	if "" != self._summary {
		return self._summary
	}
	if self.next <= 0 {
		return ""
	}

	// determine caching keys
	var start int
	if self.next > traceSize {
		start = traceSize - 1
	} else {
		start = self.next - 1
	}
	k1 := self.pcs[start]
	k2 := self.msgs[start]

	smr, rs := summaryCache.Get(k1, k2)
	switch rs {
	case cchMiss:
		self._summary = self.genSummary()
	case cchMissCacheNew:
		self._summary = self.genSummary()
		summaryCache.Set(k1, k2, self._summary)
	case cchHit:
		self._summary = smr
	}

	return self._summary
}

// Trace returns the full formatted traceback for this error.
// The result is cached in _trace and backed by traceCache.
// Implements the Error interface; also reachable via %+v formatting.
func (self *errTrace) Trace() string {
	self.mut.Lock()
	defer self.mut.Unlock()

	if "" != self._trace {
		return self._trace
	}
	if self.next <= 0 {
		return ""
	}

	// determine caching keys
	k1 := traceL1Key{turnCount: self.next, pcs: self.pcs}
	k2 := traceL2Key{cause: self.causeString(), msgs: self.msgs}

	trc, rs := traceCache.Get(k1, k2)
	switch rs {
	case cchMiss:
		self._trace = self.genTrace()
	case cchMissCacheNew:
		self._trace = self.genTrace()
		traceCache.Set(k1, k2, self._trace)
	case cchHit:
		self._trace = trc
	}

	return self._trace
}

// Cause returns the root error that initiated this trace.
func (self *errTrace) Cause() error {
	self.mut.Lock()
	defer self.mut.Unlock()
	return self.cause
}

// Format implements fmt.Formatter. %+v emit Trace(), other verbs emits Error().
func (self *errTrace) Format(f fmt.State, verb rune) {
	switch verb {
	case 'v':
		if f.Flag('+') {
			io.WriteString(f, self.Trace())
		} else {
			io.WriteString(f, self.Error())
		}
	case 's':
		io.WriteString(f, self.Error())
	}
}

// Unwrap returns the error chain for errors.Is and errors.As traversal.
// If Classify has been called, the assigned sentinel is prepended so that
// errors.Is matches it before falling through to cause.
func (self *errTrace) Unwrap() []error {
	self.mut.Lock()
	defer self.mut.Unlock()
	if nil != self.class {
		return []error{self.class, self.cause}
	}

	return []error{self.cause}
}

// Classify assigns a sentinel to override this error's identity for
// errors.Is matching. Does not affect cause or the recorded trace.
func (self *errTrace) Classify(err SentinelError) {
	self.mut.Lock()
	defer self.mut.Unlock()
	self.classify(err)
}

// causeString returns the Error() string of cause, or "" if cause is nil.
// Caller must hold mut.
func (self *errTrace) causeString() string {
	if nil != self.cause {
		return self.cause.Error()
	} else {
		return ""
	}
}

// classify sets the class field. Caller must hold mut.
func (self *errTrace) classify(err SentinelError) {
	self.class = err
}

// genSummary renders the summary string from the most recent propagation step.
// Caller must hold mut.
func (self *errTrace) genSummary() string {
	// short cut to cause string if self is empty
	if 0 == self.next {
		return self.causeString()
	}

	// determine index of current pc & msg
	var start int
	if self.next > traceSize {
		start = traceSize - 1
	} else {
		start = self.next - 1
	}
	msg := self.msgs[start]

	// retrieve file/line string
	fls := getFileLines(self.pcs[start : 1+start])[0]

	return msg + fls
}

// genTrace renders the full traceback string across all recorded steps.
// When the trace has been compressed, an omission count is inserted at the
// compression point.
// Caller must hold mut.
func (self *errTrace) genTrace() string {
	cause := self.causeString()

	// short cut to cause string if self is empty
	if 0 == self.next {
		return cause
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
	var omitfmt string
	var tb strings.Builder
	tb.WriteString("Traceback [most recent call first]:\n")
	fls := getFileLines(self.pcs[:1+start])
	msgs := self.msgs
	for i := start; i >= 0; i -= 1 {

		// write trace msg
		tb.WriteString(msgs[i])

		// write trace file/line
		tb.WriteString(fls[i])

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

	return tb.String()

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

// addCallerInfo appends a (PC, msg) pair to err's propagation trace,
// compressing out middle entries when traceSize is exceeded.
// skip adjusts the runtime.Callers depth so the recorded PC points to the
// caller of Trace or TraceAt. Must be called with err.mut held when err
// was not freshly allocated.
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
	err._summary = ""
	err._trace = ""

	err.next += 1
}

// ---
// program counter resolution caching

// pcCache stores resolved program counters keyed by pckey, shared across
// all TraceAt call sites.
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
// Error() & Trace() caching

// tickInterval is the time window duration used by the package-level scClock,
// controlling how quickly summaryCache and traceCache slot states evolve.
const tickInterval = 16 * time.Second

// ticks is the shared clock driving summaryCache and traceCache.
// summaryCache keys on (mostRecentPC, mostRecentMsg) and caches Error() output.
// traceCache keys on (pcs+turnCount, cause+msgs) and caches Trace() output.
var (
	ticks        = &scClock{}
	summaryCache = &timedCache[uintptr, string, string]{clock: ticks}
	traceCache   = &timedCache[traceL1Key, traceL2Key, string]{clock: ticks}
)

func init() {
	err := ticks.Init(tickInterval)
	if nil != err {
		panic(err)
	}
}

// traceL1Key is the stable outer key for traceCache, derived from the recorded
// PCs and total turn count. Two errTrace values sharing this key took the same
// code path and map to the same cacheSlot.
type traceL1Key struct {
	pcs       [traceSize]uintptr
	turnCount int
}

// traceL2Key is the inner key within a traceCache cacheSlot, derived from the
// cause string and message array. It discriminates errors that share a code path
// but differ in cause or per-step messages.
type traceL2Key struct {
	cause string
	msgs  [traceSize]string
}
