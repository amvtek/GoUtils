package raised

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"slices"
)

// keySize is the number of bytes in an ErrorKey.
const keySize = 16

var (
	tagCause         = []byte("CS")
	tagNext          = []byte("NX")
	tagFilelineCount = []byte("FLC")
	tagFileline      = []byte("FLN")
)

// ErrorKey is a fixed-size hash derived from an error's propagation path and
// terminal error identity. Two errors sharing the same ErrorKey represent the
// same problem: identical code path and equivalent root cause.
type ErrorKey = [keySize]byte

// HashFunc is a factory function returning a new hash.Hash instance.
// The hash must produce at least keySize bytes.
type HashFunc = func() hash.Hash

// ErrorKeyer computes a stable ErrorKey for a raised Error.
// The key is derived from the error's propagation path and terminal root cause,
// independently of any dynamic context embedded in error messages.
// Key returns true only when a key could be determined.
type ErrorKeyer interface {
	// Key returns an ErrorKey and a bool indicating if the key could be determined.
	Key(error) (ErrorKey, bool)

	isErrorKeyer() bool
}

// NewErrorKeyer returns an ErrorKeyer using SHA256 as the default hash function,
// scoped to the sentinel family identified by the phantom type T.
func NewErrorKeyer[T any](ukl UnstableKeyListener) (ErrorKeyer, error) {
	// sha256 should provide better collision resistance than fnv128
	return NewSentinelErrorKeyer[T](sha256.New, ukl)
}

// UnstableKeyEvent is delivered to an UnstableKeyListener when the ErrorKey
// for a given code path could not be stably determined, indicating that the
// terminal cause varies across calls originating from the same location.
// This typically occurs when a foreign error embeds transient state — such as
// a request ID or a dynamic value — in its message, preventing key stabilisation.
//
// The most efficient fix is to call Classify on the propagating Error at the
// EntryPoint location, asserting a stable sentinel identity that overrides the
// unstable foreign cause.
type UnstableKeyEvent struct {
	// Error is the full raised Error for which a stable key could not be determined.
	Error Error

	// K1 is the stable propagation path key for Error.
	// K1 remains constant for a given code path and can be used by the listener to
	// track instability frequency per origin site.
	K1 L1Key

	// Key is the ErrorKey that was derived for Error on this call.
	// It may differ across calls originating from the same code path.
	Key ErrorKey

	// EntryPoint is the program counter of the module entry site for Error.
	// This is the recommended location at which to call Classify in order to
	// assert a stable sentinel identity and resolve the instability.
	EntryPoint uintptr
}

// UnstableKeyListener is implemented by types that wish to observe key instability.
type UnstableKeyListener interface {
	// OnUnstableKey is called each time an ErrorKey fluctuates for a given
	// code path, with the associated event.
	OnUnstableKey(UnstableKeyEvent)
}

// UnstableKeyListenerFunc is an adapter type to allow the use of ordinary
// functions as UnstableKeyListener.
type UnstableKeyListenerFunc func(UnstableKeyEvent)

// OnUnstableKey calls self.
func (self UnstableKeyListenerFunc) OnUnstableKey(evt UnstableKeyEvent) {
	self(evt)
}

// sentinelErrorKeyer is the ErrorKeyer implementation scoped to sentinel family T.
// It is immutable after construction.
type sentinelErrorKeyer[T any] struct {
	// hf is the hash factory used to compute ErrorKeys.
	hf HashFunc

	// tc caches computed ErrorKeys keyed by code path and terminal cause string,
	// amortizing the cost of hash computation and file/line resolution on hot paths.
	tc *keyCache

	// ukl is the optional listener notified on each cache miss. May be nil.
	ukl UnstableKeyListener
}

// NewSentinelErrorKeyer returns a ErrorKeyer scoped to the sentinel family identified
// by the phantom type T, using hf as the hash function.
// Returns ErrInvalidHash if hf is nil or produces fewer than keySize bytes.
func NewSentinelErrorKeyer[T any](hf HashFunc, ukl UnstableKeyListener) (ErrorKeyer, error) {
	// validate hf
	if nil == hf {
		return nil, Trace(ErrInvalidHash, "nil hash function")
	}
	h := hf()
	if h.Size() < keySize {
		return nil, Trace(ErrInvalidHash, "insufficient hash size %d < %d", h.Size(), keySize)
	}

	sk := sentinelErrorKeyer[T]{hf: hf, tc: &keyCache{clock: ticks}, ukl: ukl}

	return &sk, nil
}

// Key computes a stable ErrorKey for err. err must be a raised Error produced
// by Trace. The key is derived from the error's propagation path
// (as file/line strings) and the terminal cause resolved via UnwrapTerminal[T].
// Results are cached by code path and terminal cause string.
// Returns false if err is not a raised Error or has no resolvable terminal cause.
func (self *sentinelErrorKeyer[T]) Key(err error) (ErrorKey, bool) {
	erk := ErrorKey{}

	// abort if err is not an *errTrace
	ert, ok := err.(*errTrace)
	if !ok || nil == ert {
		return erk, false
	}

	snp := errTraceSnapshot{}
	ert.snapshot(&snp)

	// extract ert root cause (aka terminal)
	trm := UnwrapTerminal[T](&snp)
	if nil == trm {
		return erk, false
	}

	// determine caching keys

	k1 := L1Key{}
	k1[0] = snp.epc
	copy(k1[1:(1+traceSize)], snp.pcs[:])
	k1[1+traceSize] = uintptr(snp.next) // not a valid PC, used to simplify k1

	k2 := ""
	stn, ok := trm.(SentinelError)
	if ok {
		k2 = stn.Fingerprint()
	} else {
		k2 = trm.Error() // less noisy than ert.cause which can be any error...
	}

	erk, rs := self.tc.Get(k1, k2)
	if cchHit == rs {
		return erk, true
	}

	// hash ert content
	// we use simple TLV encoding to reliably separate each component
	ib := [8]byte{}
	hs := self.hf()

	// ---
	// cause component
	hs.Write(tagCause)
	binary.BigEndian.PutUint64(ib[:], uint64(len(k2)))
	hs.Write(ib[:])
	hs.Write([]byte(k2))

	// ---
	// next component
	hs.Write(tagNext)
	binary.BigEndian.PutUint64(ib[:], uint64(snp.next))
	hs.Write(ib[:])

	// ---
	// filelines component

	// hash number of fileline entries in ert code path
	flc := snp.next
	switch {
	case flc < 0:
		flc = 0
	case flc > traceSize:
		flc = traceSize
	}
	flc += 1 // pc are read from k1 which is [epc|pcs...|next]
	hs.Write(tagFilelineCount)
	binary.BigEndian.PutUint64(ib[:], uint64(flc))
	hs.Write(ib[:])

	// hash each fileline in ert code path
	// flc allows excluding next which is not a valid pc
	var info pcInfo
	for i := range flc {
		loadPCInfo(k1[i], &info)
		hs.Write(tagFileline)
		binary.BigEndian.PutUint64(ib[:], uint64(len(info.hfl)))
		hs.Write(ib[:])
		hs.Write(info.hfl)
	}

	// ---
	// copy hash in erk
	var buf [32]byte
	copy(erk[:], hs.Sum(buf[:0]))

	if rs == cchMissCacheNew {
		self.tc.Set(k1, k2, erk)
	}

	// dispatch new KeyMissEvent...
	if nil != self.ukl {
		evt := UnstableKeyEvent{Error: ert, K1: k1, Key: erk, EntryPoint: snp.entryPoint()}
		self.ukl.OnUnstableKey(evt)
	}

	return erk, true

}

func (self *sentinelErrorKeyer[T]) isErrorKeyer() bool {
	return true
}

// keyCache is a timedCache mapping (code path, terminal cause string) to ErrorKey.
type keyCache = timedCache[L1Key, string, ErrorKey]

// UnwrapTerminal returns "minimal" error obtained by recursively unwrapping err or
// casting err to Sentinel[T], SentinelError...
func UnwrapTerminal[T any](err error) error {

	// check if err wraps a Sentinel[T]
	// if yes uses it as err Cause
	s := new(Sentinel[T])
	if errors.As(err, &s) {
		return s
	}

	// check if err wraps a SentinelError
	// if yes uses it as err Cause
	var c SentinelError
	if errors.As(err, &c) {
		return c
	}

	// err does not wrap a SentinelError
	// return the "deepest" wrapped error
	// the rationale for this heuristic is that it is likely to correspond to a non typed sentinel created with errors.New...
	var checklist, checkinglist []error
	var extractlist []terminal
	var d, p int

	checklist = []error{err}
	for len(checklist) > 0 {
		d += 1
		checkinglist = make([]error, len(checklist))
		copy(checkinglist, checklist)
		checklist = checklist[:0]
		for _, chk := range checkinglist {
			p += 1
			switch ev := chk.(type) {
			case interface{ Unwrap() error }:
				checklist = append(checklist, ev.Unwrap())
			case interface{ Unwrap() []error }:
				checklist = append(checklist, ev.Unwrap()...)
			default:
				if nil != ev {
					extractlist = append(extractlist, terminal{depth: d, pos: p, err: ev})
				}
			}
		}
	}
	if 0 == len(extractlist) {
		return nil
	}

	// we sort extractlist using a custom order that makes err "root cause" minimal
	extractlist = slices.SortedFunc(slices.Values(extractlist), func(a, b terminal) int {
		switch {
		case a.depth > b.depth:
			return -1
		case a.depth < b.depth:
			return 1
		default:
			return (a.pos - b.pos)
		}
	})

	return extractlist[0].err

}

// terminal holds a candidate root cause error together with its depth and
// position in the unwrap graph, used by UnwrapTerminal to select the most
// specific leaf when no typed sentinel is found.
type terminal struct {
	depth int
	pos   int
	err   error
}
