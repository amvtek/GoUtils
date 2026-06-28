package raised

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

var (
	testK1 = L1Key{1}
	testEP = uintptr(0xdeadbeef)
)

// --- construction ------------------------------------------------------------

func TestUnstability_New_InvalidPeriod(t *testing.T) {
	_, err := NewUnstableKeyLogger(time.Second, 0, slog.LevelWarn, nil)
	if !isErr(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestUnstability_New_InvalidThreshold(t *testing.T) {
	_, err := NewUnstableKeyLogger(minPeriod, -1, slog.LevelWarn, nil)
	if !isErr(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestUnstability_New_NilLogger(t *testing.T) {
	ukl, err := NewUnstableKeyLogger(minPeriod, 0, slog.LevelWarn, nil)
	if err != nil || ukl == nil {
		t.Fatalf("want non-nil logger, got err=%v", err)
	}
}

func TestUnstability_New_Valid(t *testing.T) {
	_, logger := newCap()
	ukl, err := NewUnstableKeyLogger(minPeriod, 2, slog.LevelWarn, logger)
	if err != nil || ukl == nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- validity guard ----------------------------------------------------------

func TestUnstability_ZeroK1_Dropped(t *testing.T) {
	cap, logger := newCap()
	ukl := newLogger(t, minPeriod, 0, logger)

	ukl.OnUnstableKey(makeEvt(L1Key{}, testEP)) // zero K1
	ukl.OnUnstableKey(makeEvt(L1Key{}, testEP))

	if len(cap.records) != 0 {
		t.Fatalf("want 0 records, got %d", len(cap.records))
	}
}

func TestUnstability_ZeroEntryPoint_Dropped(t *testing.T) {
	cap, logger := newCap()
	ukl := newLogger(t, minPeriod, 0, logger)

	ukl.OnUnstableKey(makeEvt(testK1, 0)) // zero EntryPoint
	ukl.OnUnstableKey(makeEvt(testK1, 0))

	if len(cap.records) != 0 {
		t.Fatalf("want 0 records, got %d", len(cap.records))
	}
}

// --- threshold gating --------------------------------------------------------

// threshold=0: first event (mandatory cache-miss) suppressed, second logs.
func TestUnstability_Threshold0_LogsOnSecondEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)
		evt := makeEvt(testK1, testEP)

		ukl.OnUnstableKey(evt) // suppressed (cache-miss)
		if len(cap.records) != 0 {
			t.Fatalf("want 0 records after first event, got %d", len(cap.records))
		}

		ukl.OnUnstableKey(evt) // logged
		synctest.Wait()
		if len(cap.records) != 1 {
			t.Fatalf("want 1 record after second event, got %d", len(cap.records))
		}
	})
}

// threshold=N: first N+1 events suppressed, N+2-th logs.
func TestUnstability_ThresholdN_SuppressesFirstNPlusOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const threshold = 3
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, threshold, logger)
		evt := makeEvt(testK1, testEP)

		fireN(ukl, evt, threshold+1) // all suppressed
		if len(cap.records) != 0 {
			t.Fatalf("want 0 records, got %d", len(cap.records))
		}

		ukl.OnUnstableKey(evt) // crosses threshold
		synctest.Wait()
		if len(cap.records) != 1 {
			t.Fatalf("want 1 record, got %d", len(cap.records))
		}
	})
}

// Two K1s have independent counters.
func TestUnstability_IndependentK1Counters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)

		k1a, k1b := L1Key{1}, L1Key{2}
		ep1, ep2 := uintptr(0x1), uintptr(0x2)

		ukl.OnUnstableKey(makeEvt(k1a, ep1)) // suppressed for k1a
		if len(cap.records) != 0 {
			t.Fatalf("k1a: want 0, got %d", len(cap.records))
		}

		ukl.OnUnstableKey(makeEvt(k1b, ep2)) // suppressed for k1b (independent)
		if len(cap.records) != 0 {
			t.Fatalf("k1b: want 0, got %d", len(cap.records))
		}
	})
}

// --- cooldown gating ---------------------------------------------------------

// Second event logs once; further events within period are suppressed.
func TestUnstability_Cooldown_SuppressesWithinPeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)
		evt := makeEvt(testK1, testEP)

		fireN(ukl, evt, 2) // first log emitted on second event
		synctest.Wait()
		before := len(cap.records)

		fireN(ukl, evt, 5) // still within cooldown
		synctest.Wait()
		if len(cap.records) != before {
			t.Fatalf("want no new records within cooldown, got %d extra", len(cap.records)-before)
		}
	})
}

// After period elapses a new notification is emitted.
func TestUnstability_Cooldown_LogsAfterPeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)
		evt := makeEvt(testK1, testEP)

		fireN(ukl, evt, 2) // first notification
		synctest.Wait()

		time.Sleep(minPeriod + time.Second) // advance past cooldown

		ukl.OnUnstableKey(evt) // second notification
		synctest.Wait()
		if len(cap.records) != 2 {
			t.Fatalf("want 2 records, got %d", len(cap.records))
		}
	})
}

// Two entry points have independent cooldowns.
func TestUnstability_Cooldown_IndependentEntryPoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)

		k1a, k1b := L1Key{1}, L1Key{2}
		ep1, ep2 := uintptr(0x1), uintptr(0x2)

		fireN(ukl, makeEvt(k1a, ep1), 2) // logs for ep1
		synctest.Wait()
		after1 := len(cap.records)

		fireN(ukl, makeEvt(k1b, ep2), 2) // logs for ep2 independently
		synctest.Wait()
		if len(cap.records) != after1+1 {
			t.Fatalf("want %d records, got %d", after1+1, len(cap.records))
		}
	})
}

// --- concurrency -------------------------------------------------------------

// Many goroutines crossing the threshold concurrently: log emitted at least
// once and CAS ensures it is not emitted more than once per cooldown window.
func TestUnstability_Concurrent_ExactlyOncePerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)
		evt := makeEvt(testK1, testEP)

		const goroutines = 50
		for range goroutines {
			go ukl.OnUnstableKey(evt)
		}
		synctest.Wait()

		if len(cap.records) == 0 {
			t.Fatal("want at least 1 record, got 0")
		}
		if len(cap.records) > 1 {
			t.Fatalf("want exactly 1 record, got %d", len(cap.records))
		}
	})
}

// --- log output --------------------------------------------------------------

func TestUnstability_LogOutput_Attributes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl := newLogger(t, minPeriod, 0, logger)
		evt := makeEvt(testK1, testEP)

		fireN(ukl, evt, 2)
		synctest.Wait()

		if len(cap.records) == 0 {
			t.Fatal("want 1 record, got 0")
		}
		r := cap.records[0]

		var hasEvent, hasPC, hasPosition bool

		for _, attr := range cap.attrs {
			if attr.Key == "raised.event" {
				hasEvent = true
				break
			}
		}

		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "pc":
				hasPC = true
			case "position":
				hasPosition = true
			}
			return true
		})
		if !hasEvent {
			t.Error("missing raised.event attribute")
		}
		if !hasPC {
			t.Error("missing pc attribute")
		}
		if !hasPosition {
			t.Error("missing position attribute")
		}
	})
}

func TestUnstability_LogOutput_Level(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cap, logger := newCap()
		ukl, _ := NewUnstableKeyLogger(minPeriod, 0, slog.LevelDebug, logger)
		evt := makeEvt(testK1, testEP)

		fireN(ukl, evt, 2)
		synctest.Wait()

		if len(cap.records) == 0 {
			t.Fatal("want 1 record, got 0")
		}
		if cap.records[0].Level != slog.LevelDebug {
			t.Fatalf("want Debug level, got %v", cap.records[0].Level)
		}
	})
}

// --- helpers -----------------------------------------------------------------

// capHandler captures every log record emitted into it.
type capHandler struct {
	attrs   []slog.Attr
	records []slog.Record
}

func (h *capHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *capHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(h.attrs, attrs...)
	return h
}
func (h *capHandler) WithGroup(_ string) slog.Handler { return h }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func newCap() (*capHandler, *slog.Logger) {
	h := &capHandler{}
	return h, slog.New(h)
}

// makeEvt builds a minimal valid UnstableKeyEvent with distinct K1 and EntryPoint.
func makeEvt(k1 L1Key, ep uintptr) UnstableKeyEvent {
	return UnstableKeyEvent{K1: k1, EntryPoint: ep}
}

// fireN calls OnUnstableKey n times with the same event.
func fireN(ukl *UnstableKeyLogger, evt UnstableKeyEvent, n int) {
	for range n {
		ukl.OnUnstableKey(evt)
	}
}

// newLogger is a shorthand for a valid UnstableKeyLogger with threshold=0.
func newLogger(t *testing.T, period time.Duration, threshold int64, logger *slog.Logger) *UnstableKeyLogger {
	t.Helper()
	ukl, err := NewUnstableKeyLogger(period, threshold, slog.LevelWarn, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ukl
}

// isErr reports whether err (or its chain) matches target via errors.Is.
func isErr(err, target error) bool {
	e := err
	for e != nil {
		if e == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	// use raised Cause if available
	type causer interface{ Cause() error }
	if c, ok := err.(causer); ok {
		return isErr(c.Cause(), target)
	}
	return false
}
