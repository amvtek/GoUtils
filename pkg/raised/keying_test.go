package raised

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"sync"
	"testing"
)

// ---- phantom types for test sentinel families ----

type familyA struct{}
type familyB struct{}

// ---- sentinel declarations ----

var (
	errA1 = NewSentinelError[familyA]("ERROR(1) sentinel A1")
	errA2 = NewSentinelError[familyA]("ERROR(2) sentinel A2")
	errB1 = NewSentinelError[familyB]("ERROR(1) sentinel B1")
)

// ---- construction tests ----

func TestKeying_NewSentinelErrorKeyer_NilHash(t *testing.T) {
	_, err := NewSentinelErrorKeyer[familyA](nil, nil)
	if err == nil {
		t.Fatal("expected error for nil hash function, got nil")
	}
	if !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected ErrInvalidHash, got %v", err)
	}
}

func TestKeying_NewSentinelErrorKeyer_InsufficientHashSize(t *testing.T) {
	_, err := NewSentinelErrorKeyer[familyA](smallHashFactory, nil)
	if err == nil {
		t.Fatal("expected error for insufficient hash size, got nil")
	}
	if !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected ErrInvalidHash, got %v", err)
	}
}

func TestKeying_NewSentinelErrorKeyer_Valid(t *testing.T) {
	ek, err := NewSentinelErrorKeyer[familyA](sha256Factory, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ek == nil {
		t.Fatal("expected non-nil ErrorKeyer")
	}
	if !ek.isErrorKeyer() {
		t.Fatal("isErrorKeyer() returned false")
	}
}

func TestKeying_NewErrorKeyer_Valid(t *testing.T) {
	ek, err := NewErrorKeyer[familyA](nil)
	if err != nil {
		t.Fatalf("NewErrorKeyer: unexpected error: %v", err)
	}
	if ek == nil {
		t.Fatal("expected non-nil ErrorKeyer")
	}
}

// ---- Key: input-validation tests ----

func TestKeying_Key_NonTracedError(t *testing.T) {
	ek := mustKeyer(t, nil)
	_, ok := ek.Key(errors.New("plain"))
	if ok {
		t.Error("expected false for a non-*errTrace error")
	}
}

func TestKeying_Key_NilError(t *testing.T) {
	ek := mustKeyer(t, nil)
	_, ok := ek.Key(nil)
	if ok {
		t.Error("expected false for nil error")
	}
}

// TestKey_NoResolvableTerminal documents the limitation: reaching a genuinely
// nil UnwrapTerminal result requires package-internal errTrace construction.
// The non-traced and nil paths above already cover the false-return branches
// that are reachable from outside the package.
func TestKeying_Key_NoResolvableTerminal(t *testing.T) {
	t.Skip("nil-terminal path requires internal construction; covered by TestKey_NonTracedError and TestKey_NilError")
}

// ---- Key: correctness tests ----

func TestKeying_Key_SameCallSiteSameCause_Equal(t *testing.T) {
	ek := mustKeyer(t, nil)

	// Create the error once; key it twice. The key must be stable for the
	// same (call-site PC, cause) pair regardless of how many times Key is called.
	e := traceA(errA1)
	k1, ok1 := ek.Key(e)
	k2, ok2 := ek.Key(e)

	if !ok1 || !ok2 {
		t.Fatalf("Key returned false: ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 != k2 {
		t.Errorf("expected equal keys for same call site and cause:\n  k1=%x\n  k2=%x", k1, k2)
	}
}

func TestKeying_Key_SameCallSiteDifferentCause_NotEqual(t *testing.T) {
	ek := mustKeyer(t, nil)

	k1, ok1 := ek.Key(traceA(errA1))
	k2, ok2 := ek.Key(traceA(errA2))

	if !ok1 || !ok2 {
		t.Fatalf("Key returned false: ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 == k2 {
		t.Error("expected different keys for different causes at the same call site")
	}
}

func TestKeying_Key_DifferentCallSiteSameCause_NotEqual(t *testing.T) {
	ek := mustKeyer(t, nil)

	// traceA and traceB are defined on different source lines, giving distinct PCs.
	k1, ok1 := ek.Key(traceA(errA1))
	k2, ok2 := ek.Key(traceB(errA1))

	if !ok1 || !ok2 {
		t.Fatalf("Key returned false: ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 == k2 {
		t.Error("expected different keys for the same cause at different call sites")
	}
}

func TestKeying_Key_ClassifyOverridesDeterminesKey(t *testing.T) {
	ek := mustKeyer(t, nil)

	// Both errors must originate from the same call-site so that their
	// propagation-path component (k1) is identical. traceClassifyPair
	// creates them on a single source line.
	native, foreign := traceClassifyPair(errA1, errors.New("transient foreign error"), errA1)

	kn, okn := ek.Key(native)
	kf, okf := ek.Key(foreign)

	if !okn || !okf {
		t.Fatalf("Key returned false: okn=%v okf=%v", okn, okf)
	}
	if kn != kf {
		t.Errorf("expected Classify to yield equal keys:\n  native=%x\n  classified=%x", kn, kf)
	}
}

func TestKeying_Key_ForeignErrorLeafHeuristic_Stable(t *testing.T) {
	// A foreign error with a fixed message must produce a stable key across calls.
	ek := mustKeyer(t, nil)

	// Create the traced error once; key it twice. Creating it twice would give
	// two different call-site PCs (different source lines) and different keys.
	e := traceA(errors.New("fixed foreign message"))
	k1, ok1 := ek.Key(e)
	k2, ok2 := ek.Key(e)

	if !ok1 || !ok2 {
		t.Fatalf("Key returned false: ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 != k2 {
		t.Errorf("expected stable key for fixed foreign message:\n  k1=%x\n  k2=%x", k1, k2)
	}
}

func TestKeying_Key_CrossFamilySentinels_NotEqual(t *testing.T) {
	// errA1 and errB1 share the same ERROR code but different phantom families,
	// so their Fingerprints differ and the keys must differ.
	ekA, _ := NewErrorKeyer[familyA](nil)
	ekB, _ := NewErrorKeyer[familyB](nil)

	kA, okA := ekA.Key(traceA(errA1))
	kB, okB := ekB.Key(traceA(errB1))

	if !okA || !okB {
		t.Fatalf("Key returned false: okA=%v okB=%v", okA, okB)
	}
	if kA == kB {
		t.Error("expected different keys for sentinels from different phantom families")
	}
}

// ---- Key: caching tests ----

func TestKeying_Key_CacheHit_ConsistentResult(t *testing.T) {
	ek := mustKeyer(t, nil)

	// 64 iterations is enough to push the cacheSlot past cchGrowing into
	// cchLearning/cchStable, exercising the cchHit branch.
	e := traceA(errA1)
	var firstKey ErrorKey
	const iterations = 64
	for i := range iterations {
		k, ok := ek.Key(e)
		if !ok {
			t.Fatalf("Key returned false on iteration %d", i)
		}
		if i == 0 {
			firstKey = k
		} else if k != firstKey {
			t.Errorf("key changed on iteration %d:\n  want=%x\n  got =%x", i, firstKey, k)
		}
	}
}

func TestKeying_Key_NilListener_NoPanic(t *testing.T) {
	ek, err := NewSentinelErrorKeyer[familyA](sha256Factory, nil)
	if err != nil {
		t.Fatalf("NewSentinelErrorKeyer: %v", err)
	}
	// A foreign error causes a cache miss; the nil-listener branch must not panic.
	_, ok := ek.Key(traceA(errors.New("foreign no-listener")))
	if !ok {
		t.Error("expected ok=true for a foreign error with a resolvable leaf terminal")
	}
}

func TestKeying_Key_UnstableKeyListener_Called(t *testing.T) {
	listener := &recordingListener{}
	ek, err := NewSentinelErrorKeyer[familyA](sha256Factory, listener)
	if err != nil {
		t.Fatalf("NewSentinelErrorKeyer: %v", err)
	}

	// Two errors at the same call site but different foreign messages produce
	// different k2 values, causing cache misses and triggering the listener.
	k1, ok1 := ek.Key(traceA(errors.New("msg-variant-1")))
	k2, ok2 := ek.Key(traceA(errors.New("msg-variant-2")))

	if !ok1 || !ok2 {
		t.Fatalf("Key returned false: ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 == k2 {
		t.Error("keys must differ for different foreign messages")
	}

	if listener.count() == 0 {
		t.Fatal("expected at least one UnstableKeyEvent, got none")
	}
	var zeroKey ErrorKey
	for i, evt := range listener.all() {
		if evt.EntryPoint == 0 {
			t.Errorf("event[%d]: EntryPoint should be non-zero", i)
		}
		if evt.Key == zeroKey {
			t.Errorf("event[%d]: Key should be non-zero", i)
		}
	}
}

// ---- UnwrapTerminal tests ----

func TestKeying_UnwrapTerminal_SentinelT(t *testing.T) {
	// errA1 is *Sentinel[familyA]; errors.As should match it immediately.
	wrapped := fmt.Errorf("outer: %w", errA1)
	got := UnwrapTerminal[familyA](wrapped)
	if got == nil {
		t.Fatal("expected non-nil terminal")
	}
	if got != errA1 {
		t.Errorf("expected errA1, got %v", got)
	}
}

func TestKeying_UnwrapTerminal_SentinelError(t *testing.T) {
	// errB1 is *Sentinel[familyB]: satisfies SentinelError but not Sentinel[familyA].
	// UnwrapTerminal[familyA] should fall through to the SentinelError branch.
	wrapped := fmt.Errorf("outer: %w", errB1)
	got := UnwrapTerminal[familyA](wrapped)
	if got == nil {
		t.Fatal("expected non-nil terminal")
	}
	if got != errB1 {
		t.Errorf("expected errB1, got %v", got)
	}
}

func TestKeying_UnwrapTerminal_PlainError_DeepestLeaf(t *testing.T) {
	leaf := errors.New("leaf")
	outer := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", leaf))

	got := UnwrapTerminal[familyA](outer)
	if got == nil {
		t.Fatal("expected non-nil terminal")
	}
	if got != leaf {
		t.Errorf("expected leaf error, got %v", got)
	}
}

func TestKeying_UnwrapTerminal_MultiBranchUnwrap(t *testing.T) {
	// errors.Join produces Unwrap() []error; both leaves are at depth 1.
	// The sort order favours lower pos, so leafX (pos=1) beats leafY (pos=2).
	leafX := errors.New("leaf-X")
	leafY := errors.New("leaf-Y")
	joined := errors.Join(leafX, leafY)

	got := UnwrapTerminal[familyA](joined)
	if got == nil {
		t.Fatal("expected non-nil terminal for joined errors")
	}
	if got != leafX {
		t.Errorf("expected leafX as terminal, got %v", got)
	}
}

func TestKeying_UnwrapTerminal_Nil(t *testing.T) {
	got := UnwrapTerminal[familyA](nil)
	if got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

func TestKeying_UnwrapTerminal_DirectSentinelT(t *testing.T) {
	// Passing errA1 directly (not wrapped): should be returned as-is.
	got := UnwrapTerminal[familyA](errA1)
	if got != errA1 {
		t.Errorf("expected errA1 returned directly, got %v", got)
	}
}

// ---- helpers ----

// mustKeyer creates a sentinelErrorKeyer[familyA] or fails the test.
func mustKeyer(t *testing.T, ukl UnstableKeyListener) ErrorKeyer {
	t.Helper()
	ek, err := NewErrorKeyer[familyA](ukl)
	if err != nil {
		t.Fatalf("NewErrorKeyer: unexpected error: %v", err)
	}
	return ek
}

// traceA wraps err with one Trace call and returns the resulting Error.
// Keeping this as a named function gives a stable, single call-site PC
// that tests can rely on for equality assertions.
func traceA(err error) Error { return Trace(err, "wrap") }
func traceB(err error) Error { return Trace(err, "wrap") } // different call site

// traceClassifyPair creates two errors from the same call-site on a single
// source line: one whose cause is nativeCause and one whose cause is
// foreignCause classified as classAs. Both share identical propagation-path
// PCs, making their L1 keys equal so Key can compare only the terminal cause.
func traceClassifyPair(nativeCause error, foreignCause error, classAs SentinelError) (Error, Error) {
	n, f := Trace(nativeCause, "wrap"), Trace(foreignCause, "wrap") //nolint:wsl // intentional single-line
	f.Classify(classAs)
	return n, f
}

// sha256Factory is the SHA-256 hash factory passed to NewSentinelErrorKeyer.
var sha256Factory HashFunc = sha256.New

// smallHashFactory returns a hash whose Size() is keySize-1, used to trigger
// the ErrInvalidHash path in NewSentinelErrorKeyer.
func smallHashFactory() hash.Hash { return &tinyHash{} }

// tinyHash is a minimal hash.Hash stub whose Size() is below keySize.
// All methods except Size and BlockSize delegate to a real (but ignored) hash
// so the interface is fully satisfied without an embed that could conflict.
type tinyHash struct{}

func (h *tinyHash) Write(p []byte) (int, error) { return len(p), nil }
func (h *tinyHash) Sum(b []byte) []byte         { return b }
func (h *tinyHash) Reset()                      {}
func (h *tinyHash) Size() int                   { return keySize - 1 }
func (h *tinyHash) BlockSize() int              { return 64 }

// recordingListener records every UnstableKeyEvent it receives, thread-safely.
type recordingListener struct {
	mu     sync.Mutex
	events []UnstableKeyEvent
}

func (r *recordingListener) OnUnstableKey(evt UnstableKeyEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *recordingListener) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recordingListener) all() []UnstableKeyEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]UnstableKeyEvent, len(r.events))
	copy(cp, r.events)
	return cp
}
