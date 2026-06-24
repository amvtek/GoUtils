package raised

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

var errTraceSentinel = NewSentinel("ERROR(99): trace test sentinel")
var errClassifySentinel = NewSentinel("ERROR(100): classify sentinel")

func TestError_NilPassthrough(t *testing.T) {
	err := Trace(nil, "msg")
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestError_CreatesFromPlainError(t *testing.T) {
	err := Trace(errTraceSentinel, "step one")

	et, ok := err.(*errTrace)
	if !ok {
		t.Fatal("expected err to implement Error interface")
	}
	if et.Cause() != errTraceSentinel {
		t.Errorf("expected Cause() == errTraceSentinel, got %v", et.Cause())
	}
	if et.Error() == "" {
		t.Error("expected non-empty Error()")
	}
}

func TestError_ExtendsExistingTrace(t *testing.T) {
	err := f03()

	et, ok := err.(*errTrace)
	if !ok {
		t.Fatal("expected err to implement Error interface")
	}

	trace := et.Trace()
	if !strings.Contains(trace, "failed f01") {
		t.Errorf("expected trace to contain %q, got:\n%s", "failed f01", trace)
	}
	if !strings.Contains(trace, "failed f02") {
		t.Errorf("expected trace to contain %q, got:\n%s", "failed f02", trace)
	}

	// two distinct file/line entries expected
	count := strings.Count(trace, "file:")
	if count < 2 {
		t.Errorf("expected at least 2 file/line entries in trace, got %d:\n%s", count, trace)
	}
}

func TestError_FormatVerbs(t *testing.T) {
	err := f03()

	et, ok := err.(*errTrace)
	if !ok {
		t.Fatal("expected err to implement Error interface")
	}

	if fmt.Sprintf("%s", err) != et.Error() {
		t.Errorf("%%s output does not match Error()")
	}
	if fmt.Sprintf("%v", err) != et.Error() {
		t.Errorf("%%v output does not match Error()")
	}
	if fmt.Sprintf("%+v", err) != et.Trace() {
		t.Errorf("%%+v output does not match Trace()")
	}
}

func TestError_Classify(t *testing.T) {
	err := f03()

	et, ok := err.(*errTrace)
	if !ok {
		t.Fatal("expected err to be an errTrace")
	}
	et.Classify(errClassifySentinel)

	if !errors.Is(err, errClassifySentinel) {
		t.Error("expected errors.Is to match errClassifySentinel after Classify")
	}
	if !errors.Is(err, errOne) {
		t.Error("expected errors.Is to still match original cause errOne after Classify")
	}
}

func TestError_Compression(t *testing.T) {
	// produce a chain longer than traceSize to trigger compression
	errFunc := makeRaisedPropChain(16, traceSize+4, errPropSentinel)
	err := errFunc()

	et, ok := err.(*errTrace)
	if !ok {
		t.Fatal("expected err to be an errTrace")
	}

	trace := et.Trace()
	if !strings.Contains(trace, "omission") {
		t.Errorf("expected omission marker in compressed trace, got:\n%s", trace)
	}

	// first and last step messages are "[0]: ..." and "[traceSize+3]: ..."
	first := "[0]:"
	last := fmt.Sprintf("[%d]:", traceSize+3)
	if !strings.Contains(trace, first) {
		t.Errorf("expected first step %q to be present in trace, got:\n%s", first, trace)
	}
	if !strings.Contains(trace, last) {
		t.Errorf("expected last step %q to be present in trace, got:\n%s", last, trace)
	}
}

func TestError_Show(t *testing.T) {
	errFunc := makeRaisedPropChain(16, traceSize+8, errPropSentinel)
	err := errFunc()

	t.Logf("err.Error() ->\n%v", err)
	t.Log("---")
	t.Logf("err.Trace() ->\n%+v", err)
}

func TestError_FormatArgs(t *testing.T) {
	err := Trace(errTraceSentinel, "value is %d", 42)

	if !strings.Contains(err.Error(), "value is 42") {
		t.Errorf("expected Error() to contain %q, got %q", "value is 42", err.Error())
	}
}

func TestError_TraceAtEquivalence(t *testing.T) {
	err0 := traceStep(0, errTraceSentinel, "equivalence check")
	err1 := traceStep(1, errTraceSentinel, "equivalence check")

	if err0.Error() != err1.Error() {
		t.Errorf("expected TraceAt and Trace to produce same Error() output\nTraceAt: %q\nTrace:   %q",
			err0.Error(), err1.Error())
	}
}

func TestError_ErrorsIs(t *testing.T) {
	err := f03()

	if !errors.Is(err, errOne) {
		t.Error("expected errors.Is to match errOne through the trace chain")
	}
}

func traceStep(flk int, cause error, msg string) *errTrace {
	err := TraceAt(flk, cause, msg)
	return err.(*errTrace)
}
