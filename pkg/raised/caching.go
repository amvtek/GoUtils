package raised

import (
	"sync"
	"time"
)

// maxCached is the ring buffer capacity of each cacheSlot.
const maxCached = 16

// Tuning constants for the sliding-window hit/miss accounting.
// timeWindowSize is the number of discrete time slots in the window.
// maxUseGrowing and maxUseLearning are the use-count thresholds that
// drive state transitions. minHitStable is the minimum hit count
// required within the window for a slot to be considered stable.
const (
	timeWindowSize = 8
	maxUseGrowing  = maxCached
	maxUseLearning = 2 * maxUseGrowing
	minHitStable   = 1
)

// Cache state constants. A cacheSlot advances through these states as
// hit/miss activity accumulates within the sliding time window.
//
//   - cchGrowing:  new entries are accepted and statistics are updated.
//   - cchLearning: no new entries; statistics are still updated.
//   - cchStable:   no new entries; statistics are frozen.
//   - cchDisabled: cache is bypassed entirely; lookups always miss.
const (
	cchGrowing = iota
	cchLearning
	cchStable
	cchDisabled
)

// Cache lookup status constants returned by Get and get.
//
//   - cchMiss:         value absent; caller should not attempt to cache.
//   - cchMissCacheNew: value absent; caller should generate and cache it.
//   - cchHit:          value present and returned.
const (
	cchMiss = iota
	cchMissCacheNew
	cchHit
)

// timedCache is a two-level concurrent cache keyed by (K1, K2).
// K1 is a stable outer key mapped to a cacheSlot; K2 is an inner key
// that discriminates entries within a slot. V is the cached value type.
// The cache adapts its write policy over time based on observed hit rates.
type timedCache[K1 comparable, K2 comparable, V any] struct {
	clock timed
	cache sync.Map
}

// Get looks up the value at (k1, k2) and returns it together with a
// lookup status. The status indicates whether the value was found
// (cchHit), absent with caching advised (cchMissCacheNew), or absent
// with caching not advised (cchMiss). The caller should generate the
// value and call Set only when the status is cchMissCacheNew.
//
// Note: the slot state is evaluated twice — once under a read lock and
// once when updating statistics. The state may advance between the two
// evaluations; this is intentional and the resulting inconsistency is
// benign (see setStat).
func (self *timedCache[K1, K2, V]) Get(k1 K1, k2 K2) (V, int) {
	var slot *cacheSlot[K2, V]
	var ok bool
	sv, found := self.cache.Load(k1)
	if found {
		slot, ok = sv.(*cacheSlot[K2, V])
		if !ok {
			slot = nil
			self.cache.CompareAndDelete(k1, sv) // sv should not be in cache
		}
	}

	var rv V
	var rs int
	if nil == slot {
		// slot is missing
		rs = cchMissCacheNew
		return rv, rs
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

	rv, rs = slot.get(k2)
	hit := (rs == cchHit)
	switch slot.state() {
	case cchGrowing, cchLearning:
		releaseRLock()
		slot.setStat(hit)
	}

	return rv, rs

}

// Set stores val at (k1, k2). The entry is written only if the
// underlying slot is in the cchGrowing state; calls in any other
// state are silently ignored.
func (self *timedCache[K1, K2, V]) Set(k1 K1, k2 K2, val V) {
	// retrieve k1 slot
	var slot *cacheSlot[K2, V]
	var ok bool
	sv, found := self.cache.Load(k1)
	if found {
		slot, ok = sv.(*cacheSlot[K2, V])
		if !ok {
			slot = nil
			self.cache.CompareAndDelete(k1, sv) // sv should not be in cache
		}
	}
	if nil == slot {
		sv, _ = self.cache.LoadOrStore(k1, self.newSlot())
		slot = sv.(*cacheSlot[K2, V]) // if sv was again invalid this would indicate a big issue, better panic in this case...
	}

	slot.setVal(k2, val)
}

// newSlot allocates a fresh cacheSlot bound to the cache clock.
func (self *timedCache[K1, K2, V]) newSlot() *cacheSlot[K2, V] {
	return &cacheSlot[K2, V]{clock: self.clock}
}

// cacheSlot holds up to maxCached (K, V) pairs in a ring buffer and
// tracks hit/miss statistics over a sliding time window. The statistics
// drive state transitions that control whether new entries are accepted.
// All field access is guarded by mut; callers must document their locking
// discipline when calling methods directly.
type cacheSlot[K comparable, V any] struct {
	mut   sync.RWMutex
	clock timed
	stats [timeWindowSize]cchStats
	keys  [maxCached]K
	vals  [maxCached]V
	next  int // ring buffer insertion point
}

// state derives the current cache state from hit/miss counts within the
// sliding time window. Caller must hold mut for reading.
func (self *cacheSlot[K, V]) state() int {
	ts := self.clock.T()

	var hit, miss uint32
	for i := range timeWindowSize {
		dt := ts - self.stats[i].t
		if (dt < 0) || (dt >= timeWindowSize) {
			continue
		}
		hit += self.stats[i].hit
		miss += self.stats[i].miss
	}
	use := hit + miss

	switch {
	case (use < maxUseGrowing):
		return cchGrowing
	case (use < maxUseLearning):
		return cchLearning
	case (hit >= minHitStable):
		return cchStable
	default:
		return cchDisabled
	}
}

// get looks up ck in the ring buffer and returns the associated value
// and a lookup status.
// Caller must hold mut for reading.
func (self *cacheSlot[K, V]) get(ck K) (V, int) {
	for i := range min(self.next, maxCached) {
		if ck == self.keys[i] {
			return self.vals[i], cchHit
		}
	}

	var zv V
	status := cchMiss
	if self.state() == cchGrowing {
		status = cchMissCacheNew
	}

	return zv, status
}

// setStat records a hit or miss in the current time window bucket.
// No-ops when the slot is in cchStable or cchDisabled state.
func (self *cacheSlot[K, V]) setStat(hit bool) {
	self.mut.Lock()
	defer self.mut.Unlock()

	switch self.state() {
	case cchStable, cchDisabled:
		return
	}

	ts := self.clock.T()
	idx := ts % timeWindowSize
	if idx < 0 {
		idx += timeWindowSize
	}
	stat := self.stats[idx]
	if ts != stat.t {
		stat = cchStats{t: ts}
	}
	if hit {
		stat.hit += 1
	} else {
		stat.miss += 1
	}
	self.stats[idx] = stat
}

// setVal stores (ck, val) in the ring buffer. No-ops if the slot is
// not in cchGrowing state, or if ck is already present (updates the
// existing entry in that case).
func (self *cacheSlot[K, V]) setVal(ck K, val V) {
	self.mut.Lock()
	defer self.mut.Unlock()

	if self.state() != cchGrowing {
		return
	}

	for i := range min(self.next, maxCached) {
		if ck == self.keys[i] {
			self.vals[i] = val
			return
		}
	}
	self.keys[self.next%maxCached] = ck
	self.vals[self.next%maxCached] = val
	self.next += 1
}

// cchStats holds hit and miss counts for a single time window bucket.
// t is the window tick at which the counts were recorded.
type cchStats struct {
	t    int64
	hit  uint32
	miss uint32
}

// reset clears hit and miss counts if t differs from the stored tick,
// then updates the stored tick to t.
func (self *cchStats) reset(t int64) {
	if t != self.t {
		self.t = t
		self.hit = 0
		self.miss = 0
	}
}

// timed is a monotonic pseudo-clock interface. Implementations must
// return a non-decreasing integer that advances at a predictable rate.
type timed interface {
	// T returns a pseudo time.
	T() int64
}

// scClock is a scaled monotonic clock.
// it increments time by 1 every step nanoseconds.
// Initializes before use with Init.
type scClock struct {
	t0   time.Time
	step int64
}

// Init configures the clock with the given tick duration and records the
// start time. Returns ErrValidation if the receiver is nil or step <= 0.
func (self *scClock) Init(step time.Duration) error {
	if nil == self {
		return Trace(ErrValidation, "nil scClock")
	}
	if step <= 0 {
		return Trace(ErrValidation, "step <= 0")
	}
	self.t0 = time.Now()
	self.step = step.Nanoseconds()

	return nil
}

// T returns the number of complete ticks elapsed since Init was called.
func (self *scClock) T() int64 {
	return time.Since(self.t0).Nanoseconds() / self.step
}

var _ timed = &scClock{}
