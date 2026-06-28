package raised

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// minPeriod is the minimum allowed notification cooldown passed to NewUnstableKeyLogger.
	minPeriod = 10 * time.Second

	// notification is the log message emitted when an unstable error key is detected.
	notification = "Unstable error key, use Classify on traced error"
)

// eventTag is the slog attribute added to every log record emitted by UnstableKeyLogger.
var eventTag = slog.String("raised.event", "unstable-error-key")

// UnstableKeyLogger is an UnstableKeyListener that logs a diagnostic when an
// error key fluctuates for a given code path, rate-limited by a per-entry-point
// cooldown and a per-path event threshold.
type UnstableKeyLogger struct {
	prd   time.Duration
	lmt   int64
	lvl   slog.Level
	log   *slog.Logger
	regK1 sync.Map
	regK2 sync.Map
}

// NewUnstableKeyLogger returns an UnstableKeyLogger that emits notifications at
// the given level via logger, suppressing events until threshold instabilities
// have been observed on a given code path beyond the mandatory first cache-miss,
// and then at most once per period per entry point.
// Returns ErrValidation if period < minPeriod or threshold < 0.
// If logger is nil, slog.Default() is used.
func NewUnstableKeyLogger(period time.Duration, threshold int64, level slog.Level, logger *slog.Logger) (*UnstableKeyLogger, error) {

	// check period
	if period < minPeriod {
		return nil, Tracef(ErrValidation, "invalid period < %s", minPeriod)
	}

	// check threshold
	if threshold < 0 {
		return nil, Trace(ErrValidation, "invalid threshold < 0")
	}

	// limit accounts for the mandatory first cache-miss event, which is not
	// a true instability signal. threshold=0 means log from the second event.
	limit := 1 + threshold

	// log initialization
	if nil == logger {
		logger = slog.Default()
	}

	return &UnstableKeyLogger{prd: period, lmt: limit, lvl: level, log: logger.With(eventTag)}, nil
}

// OnUnstableKey implements UnstableKeyListener. It increments the event counter
// for evt.K1 and, once the threshold is exceeded, emits a rate-limited log
// record identifying the entry point at which Classify should be called.
// No-ops if evt.K1 is zero or evt.EntryPoint is zero.
func (self *UnstableKeyLogger) OnUnstableKey(evt UnstableKeyEvent) {

	// check evt validity
	zeroK1 := L1Key{}
	if zeroK1 == evt.K1 || 0 == evt.EntryPoint {
		return
	}

	var ok bool

	// check if lmt exceeded on evt.K1
	var k1c *atomic.Int64
	val, found := self.regK1.Load(evt.K1)
	if found {
		k1c, ok = val.(*atomic.Int64)
		if !ok {
			self.regK1.CompareAndDelete(evt.K1, val)
			k1c = nil
		}
	}
	if nil == k1c {
		val, _ = self.regK1.LoadOrStore(evt.K1, new(atomic.Int64))
		k1c = val.(*atomic.Int64)
	}
	if k1c.Add(1) <= self.lmt {
		return
	}

	// determine if time has come to emit new EntryPoint/Classification notification
	var k2t *atomic.Int64
	val, found = self.regK2.Load(evt.EntryPoint)
	if found {
		k2t, ok = val.(*atomic.Int64)
		if !ok {
			self.regK2.CompareAndDelete(evt.EntryPoint, val)
			k2t = nil
		}
	}
	if nil == k2t {
		val, _ = self.regK2.LoadOrStore(evt.EntryPoint, new(atomic.Int64))
		k2t = val.(*atomic.Int64)
	}
	t1 := time.Now()
	t0 := time.UnixMilli(k2t.Load())
	if t1.Compare(t0.Add(self.prd)) < 0 {
		return
	}

	// attempt to emit notification
	swapped := k2t.CompareAndSwap(t0.UnixMilli(), t1.UnixMilli())
	if !swapped {
		// t0 was modified by other goroutine...
		return
	}
	var info pcInfo
	loadPCInfo(evt.EntryPoint, &info)
	ctx := context.Background()
	codePC := slog.Uint64("pc", uint64(evt.EntryPoint))
	codePos := slog.String("position", strings.TrimSpace(info.tfl))
	self.log.LogAttrs(ctx, self.lvl, notification, codePos, codePC)

}

var _ UnstableKeyListener = &UnstableKeyLogger{}
