package raised

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ====================================================================================
// Tests
//
// Run with race detection:
//
//	go test -race -run TestTrace_ConcurrentCacheContention ./...
//	go test -race -count=10 -run TestTrace_ConcurrentCacheContention ./...

func TestConcurrent_CacheContentionTrace(t *testing.T) {
	runContendTest(t, contendTraceL0)
}

func TestConcurrent_CacheContentionTraceAt(t *testing.T) {
	runContendTest(t, contendTraceAtL0)
}

// runContendTest is the shared assertion loop used by both contention tests.
// It spawns workers goroutines each producing iterations errors via chainFn,
// collects results, and asserts sentinel, Error(), and Trace() correctness.
func runContendTest(t *testing.T, chainFn func(int) error) {
	t.Helper()
	const workers = 64
	const iterations = 16

	exp := buildContendExpectations(t, chainFn)

	type result struct {
		n   int
		err error
	}
	results := make(chan result, workers*iterations)

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range iterations {
				n := w*iterations + i
				results <- result{n: n, err: chainFn(n % contendGroupN)}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var failures int
	for r := range results {
		n, g := r.n, r.n%contendGroupN
		re, ok := r.err.(*errTrace)
		if !ok {
			t.Errorf("n=%d: expected Error interface", n)
			failures++
			continue
		}
		if !errors.Is(r.err, exp[g].sentinel) {
			t.Errorf("n=%d group=%d: errors.Is did not match expected sentinel", n, g)
			failures++
		}
		if summary := re.Error(); summary != exp[g].summary {
			t.Errorf("n=%d group=%d: Error() mismatch\nwant: %s\ngot:  %s", n, g, exp[g].summary, summary)
			failures++
		}
		if trace := re.Trace(); trace != exp[g].trace {
			t.Errorf("n=%d group=%d: Trace() mismatch\nwant: %s\ngot:  %s", n, g, exp[g].trace, trace)
			failures++
		}
		if failures > 8 {
			t.Fatal("too many failures, aborting")
		}
	}
}

// ====================================================================================
// Sentinels — n%contendGroupN selects which one.
// All errors in the same group share the same sentinel, messages, and
// therefore identical expected Error() and Trace() output.

const contendGroupN = 4

var contendSentinels = [contendGroupN]SentinelError{
	NewSentinel("ERROR(301): cache contend sentinel 0"),
	NewSentinel("ERROR(302): cache contend sentinel 1"),
	NewSentinel("ERROR(303): cache contend sentinel 2"),
	NewSentinel("ERROR(304): cache contend sentinel 3"),
}

// ====================================================================================
// Call-site chains.
//
// Two independent chains — one using Trace, one using TraceAt — both
// produce group-stable messages so that all errors with the same n%contendGroupN
// share identical Error() and Trace() output. This makes within-group errors
// true cache hit candidates, and any corruption shows up as an exact mismatch.
//
// Both chains share the same four message templates, keeping expectations
// identical across the two tests.

func contendMsg(level, group int) string {
	return fmt.Sprintf("L%d g=%d", level, group)
}

// --- Trace chain ---

func contendTraceL3(group int) error {
	return Trace(contendSentinels[group], contendMsg(3, group))
}

func contendTraceL2(group int) error {
	return Trace(contendTraceL3(group), contendMsg(2, group))
}

func contendTraceL1(group int) error {
	return Trace(contendTraceL2(group), contendMsg(1, group))
}

func contendTraceL0(group int) error {
	return Trace(contendTraceL1(group), contendMsg(0, group))
}

// --- TraceAt chain ---
//
// flk values are group-offset constants, keeping them non-zero and stable
// per group so that pcCache entries are shared across goroutines in the
// same group — exercising concurrent LoadOrStore on the same key.

func contendTraceAtL3(group int) error {
	return TraceAt(130+group, contendSentinels[group], contendMsg(3, group))
}

func contendTraceAtL2(group int) error {
	return TraceAt(120+group, contendTraceAtL3(group), contendMsg(2, group))
}

func contendTraceAtL1(group int) error {
	return TraceAt(110+group, contendTraceAtL2(group), contendMsg(1, group))
}

func contendTraceAtL0(group int) error {
	return TraceAt(100+group, contendTraceAtL1(group), contendMsg(0, group))
}

// ====================================================================================
// Shared test infrastructure

type contendExpectation struct {
	sentinel SentinelError
	summary  string
	trace    string
}

// buildContendExpectations calls chainFn(group) for each group and records
// the expected sentinel, Error(), and Trace() output. It fails fast if the
// generated values do not contain the expected level messages.
func buildContendExpectations(t *testing.T, chainFn func(int) error) [contendGroupN]contendExpectation {
	t.Helper()
	var exp [contendGroupN]contendExpectation
	for g := range contendGroupN {
		et, ok := chainFn(g).(*errTrace)
		if !ok {
			t.Fatalf("group %d: expected *errTrace", g)
		}
		summary := et.genSummary()
		trace := et.genTrace()
		for level := range 4 {
			msg := contendMsg(level, g)
			if !strings.Contains(trace, msg) {
				t.Fatalf("group %d: genTrace() missing %q\ngot:\n%s", g, msg, trace)
			}
		}
		if !strings.Contains(summary, contendMsg(0, g)) {
			t.Fatalf("group %d: genSummary() missing %q\ngot: %s", g, contendMsg(0, g), summary)
		}
		exp[g] = contendExpectation{
			sentinel: contendSentinels[g],
			summary:  summary,
			trace:    trace,
		}
	}
	return exp
}
