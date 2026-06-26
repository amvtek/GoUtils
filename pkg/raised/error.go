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

// Trace records a propagation step for err, attaching msg and the call site PC
// to the trace. If err is already a traced error it is extended in place,
// otherwise a new trace is created with err as its cause.
// Returns nil if err is nil.
func Trace(err error, msg string) Error {
	if nil == err {
		return nil
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

// Tracef records a propagation step for err, attaching msg and the call site PC
// to the trace. If err is already a traced error it is extended in place,
// otherwise a new trace is created with err as its cause.
// If args are provided, msg is used as a format string.
// Returns nil if err is nil.
func Tracef(err error, msg string, args ...any) Error {
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

// errTrace is an error used to track the propagation of a root error...
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

	// epc (entry PC) holds the first program counter that is module local.
	// due to trace compression epc may not be present in pcs.
	epc uintptr

	// pcs holds the program counter for each recorded Trace call site.
	pcs [traceSize]uintptr

	// msgs holds the message string supplied at each Trace call site, parallel to pcs.
	msgs [traceSize]string

	// _summary and _trace are lazily computed and cached renderings.
	// Both are invalidated (set to "") whenever a new propagation step is added.
	_summary string
	_trace   string
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

	self._summary = self.genSummary()

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

	self._trace = self.genTrace()

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
	var info pcInfo
	loadPCInfo(self.pcs[start], &info)

	return msg + info.tfl
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

	// prepare lines buffer (used to avoid allocating)
	var line string
	var lns [4 + 2*traceSize]string
	var size int
	lines := lns[:0]

	line = "Traceback [most recent call first]:\n"
	size += len(line)
	lines = append(lines, line)

	var omitfmt string
	var info pcInfo
	msgs := self.msgs
	pcs := self.pcs
	for i := start; i >= 0; i -= 1 {
		// buffer trace msg
		line = msgs[i]
		size += len(line)
		lines = append(lines, line)

		// buffer trace file/line
		loadPCInfo(pcs[i], &info)
		line = info.tfl
		size += len(line)
		lines = append(lines, line)

		// insert misscount if it is > 0
		if i == misspos {
			if 1 == misscount {
				omitfmt = "    [%d omission...]\n"
			} else {
				omitfmt = "    [%d omissions...]\n"
			}
			line = fmt.Sprintf(omitfmt, misscount) // 1 extra alloc, could be avoided...
			size += len(line)
			lines = append(lines, line)
		}
	}
	if cause != msgs[0] {
		line = "Caused by:\n  "
		size += len(line)
		lines = append(lines, line)

		line = cause
		size += len(line)
		lines = append(lines, line)
	}

	// render full error trace
	var tb strings.Builder
	tb.Grow(size) // only 1 alloc
	for _, ln := range lines {
		tb.WriteString(ln)
	}

	return tb.String()

}

// snapshot captures a consistent point-in-time view of the errTrace fields
// needed for error keying. The snapshot is taken under mut, ensuring that
// subsequent operations on the snapshot are race-free.
func (self *errTrace) snapshot(dst *errTraceSnapshot) {
	self.mut.Lock()
	defer self.mut.Unlock()
	dst.cause = self.cause
	dst.class = self.class
	dst.next = self.next
	dst.epc = self.epc
	dst.pcs = self.pcs
}

// errTraceSnapshot is an immutable point-in-time view of an errTrace.
type errTraceSnapshot struct {
	cause error
	class SentinelError
	next  int
	epc   uintptr
	pcs   [traceSize]uintptr
}

// Error returns the Error() string of cause, or "" if cause is nil.
func (self *errTraceSnapshot) Error() string {
	if nil != self.cause {
		return self.cause.Error()
	}
	return ""
}

// Unwrap returns the error chain for errors.As traversal.
// If class is set it is prepended, mirroring errTrace.Unwrap behaviour.
func (self *errTraceSnapshot) Unwrap() []error {
	rv := make([]error, 0, 2)
	if nil != self.class {
		rv = append(rv, self.class)
	}
	if nil != self.cause {
		rv = append(rv, self.cause)
	}

	return rv
}

// entryPoint returns the program counter of the module entry point for this trace.
// If epc was recorded, it is returned directly as it represents the first module-local
// call site encountered during error propagation, which is the most efficient location
// for a Classify call to stabilize the error Key.
// If epc is zero, falls back to the most recent PC in the trace as a best-effort approximation.
func (self *errTraceSnapshot) entryPoint() uintptr {
	if 0 != self.epc {
		return self.epc
	}

	// use most recent pc as entry point
	// we may do better iterating pcs in reverse order, looking at pc package until it is different than pcs[c]...
	c := min(self.next, traceSize) - 1
	if c >= 0 {
		return self.pcs[c]
	} else {
		return 0
	}
}

// buildModPath is the module path prefix stripped from file names in traces.
var buildModPath string

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		buildModPath = info.Main.Path + "/" // if info.Main.Path is "", buildModPath is not a valid pkgpath prefix.
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

	// record module "entry point"
	local := isLocal(pc)
	if local && err.epc == 0 {
		err.epc = pc
	}

	// clear cached summary & trace
	err._summary = ""
	err._trace = ""

	err.next += 1
}

// ---
// program counter resolution caching

// pcCache stores resolved program counters keyed by pckey, shared across
// all Trace call sites.
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

// pcInfoCache is a process-wide store mapping program counter values to their
// resolved pcInfo.
var pcInfoCache sync.Map

// pcInfo holds the resolved file/line information for a single program counter,
// cached to amortize the cost of runtime.CallersFrames across repeated lookups
// of the same PC.
type pcInfo struct {
	// pc is the program counter this entry was resolved from.
	pc uintptr

	// trace fileline string (optimised for reading)
	tfl string

	// hash fileline slice (used for error key hashing)
	hfl []byte

	// true if file is part of project
	local bool
}

// isLocal returns true if pc is a program counter within "project" module.
// isLocal has side effects, it stores file/line information in  pcInfoCache.
func isLocal(pc uintptr) bool {
	if 0 == pc {
		return false
	}

	var info pcInfo
	var ok bool

	val, found := pcInfoCache.Load(pc)
	if found {
		info, ok = val.(pcInfo)
		if !ok {
			info.pc = 0
			pcInfoCache.CompareAndDelete(pc, val)
		}
	}
	if 0 == info.pc {
		info.pc = pc

		pcs := []uintptr{pc}
		frames := runtime.CallersFrames(pcs)
		frame, _ := frames.Next()

		// extract base filename
		basename := filepath.Base(frame.File)

		// funcname in canonical form module/package.funcname[.component]
		// funcname may have more than 1 component if func is instantiated dynamically by a factory
		funcname := frame.Function

		// extract pkgpath in canonical form module/package
		dotpos := strings.LastIndex(funcname, "/")
		if dotpos < 0 {
			dotpos = 0
		}
		dotpos += strings.Index(funcname[dotpos:], ".")
		if dotpos >= 0 {
			pkgpath := funcname[:dotpos]
			filename := path.Join(pkgpath, basename)

			info.hfl = fmt.Appendf(nil, filelineFmt, filename, frame.Line)

			// filename is in canonical form module/package/filename
			// module is relative to current buildModPath
			filename, local := strings.CutPrefix(filename, buildModPath)
			info.local = local
			info.tfl = fmt.Sprintf(filelineFmt, filename, frame.Line)

		} else {

			info.tfl = missFileline
			info.hfl = []byte(missFileline)
		}
		val, _ = pcInfoCache.LoadOrStore(pc, info)
		info = val.(pcInfo) // if val was again invalid this would indicate a big issue, better panic in this case
	}

	return info.local

}

// noPCInfo is the sentinel pcInfo returned by loadPCInfo when a program counter
// cannot be resolved or is not present in pcInfoCache.
var noPCInfo = pcInfo{tfl: missFileline, hfl: []byte(missFileline)}

// loadPCInfo retrieves the cached pcInfo for pc into dst.
// Returns true if a valid entry was found in pcInfoCache.
func loadPCInfo(pc uintptr, dst *pcInfo) bool {
	var ok, valid bool
	info := noPCInfo

	val, ok := pcInfoCache.Load(pc)
	if ok {
		info, valid = val.(pcInfo)
		if !valid {
			pcInfoCache.CompareAndDelete(pc, val)
			info = noPCInfo
			ok = false
		}
	}
	*dst = info

	return ok
}

// tickInterval is the time window duration used by the package-level scClock,
// controlling how quickly summaryCache and traceCache slot states evolve.
const tickInterval = 16 * time.Second

// ticks is the shared clock driving summaryCache and traceCache.
var ticks = &scClock{}

func init() {
	err := ticks.Init(tickInterval)
	if nil != err {
		panic(err)
	}
}

// L1Key is a stable identifier for an error's propagation path, derived from
// the module entry point, the recorded propagation PCs, and the total Trace
// call count.
type L1Key = [2 + traceSize]uintptr
