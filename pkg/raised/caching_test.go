package raised

import (
	"sync"
	"testing"
	"time"
)

// ====================================================================================
// scClock tests

func TestCaching_scClockMonotonic(t *testing.T) {
	clk := &scClock{}
	if err := clk.Init(1 * time.Millisecond); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	prev := clk.T()
	for range 5 {
		time.Sleep(2 * time.Millisecond)
		next := clk.T()
		if next < prev {
			t.Errorf("clock went backwards: %d -> %d", prev, next)
		}
		prev = next
	}
}

func TestCaching_scClockInitErrors(t *testing.T) {
	cases := []struct {
		name string
		step time.Duration
	}{
		{"zero step", 0},
		{"negative step", -1 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &scClock{}
			err := clk.Init(tc.step)
			if err == nil {
				t.Errorf("expected error for step=%v, got nil", tc.step)
			}
		})
	}
}

// ====================================================================================
// cacheSlot state machine tests

func TestCaching_cacheSlotStateGrowing(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	slot.mut.RLock()
	s := slot.state()
	slot.mut.RUnlock()

	if s != cchGrowing {
		t.Errorf("expected cchGrowing, got %d", s)
	}
}

func TestCaching_cacheSlotStateLearning(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	// push use count into [maxUseGrowing, maxUseLearning)
	fillStats(clk, slot, maxUseGrowing, false)

	slot.mut.RLock()
	s := slot.state()
	slot.mut.RUnlock()

	if s != cchLearning {
		t.Errorf("expected cchLearning, got %d", s)
	}
}

func TestCaching_cacheSlotStateStable(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	// reach maxUseLearning with all hits to satisfy minHitStable
	fillStats(clk, slot, maxUseLearning, true)

	slot.mut.RLock()
	s := slot.state()
	slot.mut.RUnlock()

	if s != cchStable {
		t.Errorf("expected cchStable, got %d", s)
	}
}

func TestCaching_cacheSlotStateDisabled(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	// reach maxUseLearning with all misses — no hits to qualify as stable
	fillStats(clk, slot, maxUseLearning, false)

	slot.mut.RLock()
	s := slot.state()
	slot.mut.RUnlock()

	if s != cchDisabled {
		t.Errorf("expected cchDisabled, got %d", s)
	}
}

// ====================================================================================
// cacheSlot get / setVal tests

func TestCaching_cacheSlotGetMiss(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	slot.mut.RLock()
	_, rs := slot.get("k")
	slot.mut.RUnlock()

	if rs != cchMissCacheNew {
		t.Errorf("expected cchMissCacheNew on fresh slot, got %d", rs)
	}
}

func TestCaching_cacheSlotGetHit(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	slot.setVal("k", "v")

	slot.mut.RLock()
	val, rs := slot.get("k")
	slot.mut.RUnlock()

	if rs != cchHit {
		t.Errorf("expected cchHit, got %d", rs)
	}
	if val != "v" {
		t.Errorf("expected value %q, got %q", "v", val)
	}
}

func TestCaching_cacheSlotSetValIgnoredWhenNotGrowing(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	// advance slot past cchGrowing
	fillStats(clk, slot, maxUseGrowing, false)

	slot.setVal("k", "v")

	slot.mut.RLock()
	_, rs := slot.get("k")
	slot.mut.RUnlock()

	if rs == cchHit {
		t.Error("expected no hit: setVal should be ignored when slot is not cchGrowing")
	}
}

func TestCaching_cacheSlotRingBufferWrap(t *testing.T) {
	// Clock is frozen at 0 so all stats land in the same bucket and the
	// slot stays cchGrowing throughout, accepting all maxCached+1 writes.
	clk := newClock(0)
	slot := newTestSlot[int, int](clk)

	// store maxCached+1 distinct entries; the last one overwrites ring index 0
	for i := range maxCached + 1 {
		slot.setVal(i, i*10)
	}

	slot.mut.RLock()
	defer slot.mut.RUnlock()

	// entries 1..maxCached should all be present
	for i := 1; i <= maxCached; i++ {
		val, rs := slot.get(i)
		if rs != cchHit {
			t.Errorf("key %d: expected cchHit, got %d", i, rs)
			continue
		}
		if val != i*10 {
			t.Errorf("key %d: expected value %d, got %d", i, i*10, val)
		}
	}

	// entry 0 was overwritten by entry maxCached at ring position 0
	_, rs := slot.get(0)
	if rs == cchHit {
		t.Error("key 0: expected miss after ring buffer overwrite, got hit")
	}
}

func TestCaching_cacheSlotSlidingWindow(t *testing.T) {
	clk := newClock(0)
	slot := newTestSlot[string, string](clk)

	// fill the window with hits to reach cchStable; each stat lands in a
	// distinct bucket because fillStats advances the clock between calls
	fillStats(clk, slot, maxUseLearning, true)

	slot.mut.RLock()
	s := slot.state()
	slot.mut.RUnlock()
	if s != cchStable {
		t.Fatalf("precondition failed: expected cchStable before window expiry, got %d", s)
	}

	// jump the clock forward far enough to expire all recorded buckets.
	// fillStats writes into ticks 1..timeWindowSize; advancing by timeWindowSize
	// puts the current tick at 2*timeWindowSize, so dt for the oldest bucket
	// is timeWindowSize which fails the dt < timeWindowSize guard in state().
	clk.advance(timeWindowSize)

	slot.mut.RLock()
	s = slot.state()
	slot.mut.RUnlock()

	if s != cchGrowing {
		t.Errorf("expected cchGrowing after window expiry, got %d", s)
	}
}

// ====================================================================================
// timedCache tests

func TestCaching_timedCacheGetMissNoSlot(t *testing.T) {
	clk := newClock(0)
	tc := &timedCache[string, string, string]{clock: clk}

	_, rs := tc.Get("k1", "k2")
	if rs != cchMissCacheNew {
		t.Errorf("expected cchMissCacheNew on empty cache, got %d", rs)
	}
}

func TestCaching_timedCacheSetAndGet(t *testing.T) {
	clk := newClock(0)
	tc := &timedCache[string, string, string]{clock: clk}

	tc.Set("k1", "k2", "value")
	val, rs := tc.Get("k1", "k2")

	if rs != cchHit {
		t.Errorf("expected cchHit after Set, got %d", rs)
	}
	if val != "value" {
		t.Errorf("expected %q, got %q", "value", val)
	}
}

func TestCaching_timedCacheGetMissDifferentK2(t *testing.T) {
	clk := newClock(0)
	tc := &timedCache[string, string, string]{clock: clk}

	tc.Set("k1", "k2a", "value")
	_, rs := tc.Get("k1", "k2b")

	if rs == cchHit {
		t.Error("expected miss for different K2, got cchHit")
	}
}

// ====================================================================================
// utilities

// autoincClock is a controllable timed implementation for testing.
// T() always returns the current value without advancing it.
// Call advance to move the clock forward by an absolute amount.
type autoincClock struct {
	mu  sync.Mutex
	val int64
}

func newClock(initial int64) *autoincClock {
	return &autoincClock{val: initial}
}

func (c *autoincClock) T() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

// advance moves the clock forward by n ticks.
func (c *autoincClock) advance(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.val += n
}

var _ timed = &autoincClock{}

// newTestSlot returns a cacheSlot wired to the given clock.
func newTestSlot[K comparable, V any](clk timed) *cacheSlot[K, V] {
	return &cacheSlot[K, V]{clock: clk}
}

// fillStats drives a slot to a target use count by calling setStat.
// The clock advances once per stride calls, where stride = count/timeWindowSize,
// so all counts accumulate within the sliding window. Advancing every call
// would spread writes across more ticks than the window holds, causing
// older buckets to expire before state() can count them.
func fillStats[K comparable, V any](clk *autoincClock, slot *cacheSlot[K, V], count int, hit bool) {
	stride := count / timeWindowSize
	if stride < 1 {
		stride = 1
	}
	for i := range count {
		if i%stride == 0 {
			clk.advance(1)
		}
		slot.setStat(hit)
	}
}
