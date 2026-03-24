package raised

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unique"
)

// ====================================================================================

func TestTmp_Errorf(t *testing.T) {
	err := errors.New("ERROR(7856): Root Cause")
	for i := range 8 {
		err = fmt.Errorf("[%d] %w", i, err)
	}
	t.Logf("err -> %v", err)
	switch v := err.(type) {
	case interface{ Unwrap() error }:
		t.Logf("err.Unwrap() -> error | %v", v.Unwrap())
	case interface{ Unwrap() []error }:
		t.Logf("err.Unwrap() -> []error | %v", v.Unwrap())
	default:
		t.Logf("err.Error() -> %s", err.Error())
	}
}

func TestTmp_Join(t *testing.T) {
	err := errors.Join(
		errors.New("ERROR(10): Basic"),
		errors.New("ERROR(20): Simple"),
	)
	t.Logf("err -> %v", err)
	switch v := err.(type) {
	case interface{ Unwrap() error }:
		t.Logf("err.Unwrap() -> error | %v", v.Unwrap())
	case interface{ Unwrap() []error }:
		t.Logf("err.Unwrap() -> []error | %v", v.Unwrap())
	default:
		t.Logf("err.Error() -> %s", err.Error())
	}
}

func TestDesign_Lab(t *testing.T) {
	t.Logf("f03() -> %+v", f03())
}

var errOne = NewSentinel("ERROR(1): 1 more time...")

func f01() error {
	return errOne
}

func f02() error {
	return TraceAt(0, f01(), "failed f01")
}

func f03() error {
	return TraceAt(0, f02(), "failed f02")
}

// ====================================================================================
// Propagating fmt.Errorf vs using raised.TraceAt
//
// To run those benchmarks use:
// go test ./pkg/raised -bench Design_prop -benchmem
//
// Those benchmarks show that propagating an error using raised.Trace requires a single alloc
// Whereas fmt.Errorf number of allocs grow linearily with trace size.
// raised Error rendering time performance is also much better than what fmt.Errorf allows.

func BenchmarkDesign_propErrorfs64r4(b *testing.B) {
	ef := makeErrorfPropChain(64, 4)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaiseds64r4(b *testing.B) {
	ef := makeRaisedPropChain(64, 4)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaisedPCCaches64r4(b *testing.B) {
	ef := makeRaisedPCCachePropChain(64, 4)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propErrorfTraces64r4(b *testing.B) {
	ef := makeErrorfPropChain(64, 4)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedTraces64r4(b *testing.B) {
	ef := makeRaisedPropChain(64, 4)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedPCCacheTraces64r4(b *testing.B) {
	ef := makeRaisedPCCachePropChain(64, 4)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propErrorfs64r8(b *testing.B) {
	ef := makeErrorfPropChain(64, 8)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaiseds64r8(b *testing.B) {
	ef := makeRaisedPropChain(64, 8)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaisedPCCaches64r8(b *testing.B) {
	ef := makeRaisedPCCachePropChain(64, 8)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propErrorfTraces64r8(b *testing.B) {
	ef := makeErrorfPropChain(64, 8)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedTraces64r8(b *testing.B) {
	ef := makeRaisedPropChain(64, 8)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedPCCacheTraces64r8(b *testing.B) {
	ef := makeRaisedPCCachePropChain(64, 8)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propErrorfs256r16(b *testing.B) {
	ef := makeErrorfPropChain(256, 16)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaiseds256r16(b *testing.B) {
	ef := makeRaisedPropChain(256, 16)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propRaisedPCCaches256r16(b *testing.B) {
	ef := makeRaisedPCCachePropChain(256, 16)
	for b.Loop() {
		_ = ef()
	}
}

func BenchmarkDesign_propErrorfTraces256r16(b *testing.B) {
	ef := makeErrorfPropChain(256, 16)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedTraces256r16(b *testing.B) {
	ef := makeRaisedPropChain(256, 16)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

func BenchmarkDesign_propRaisedPCCacheTraces256r16(b *testing.B) {
	ef := makeRaisedPCCachePropChain(256, 16)
	for b.Loop() {
		_ = fmt.Sprintf("%+v", ef())
	}
}

var errPropSentinel = NewSentinel("ERROR(245): propagation sentinel")

type errfunc = func() error

func makeErrorfPropChain(strsz int, chnsz int) errfunc {
	makeNextFunc := func(fls int, prev errfunc) errfunc {
		wfmt := fmt.Sprintf("[%d]: %s", fls, rndString(strsz)) + " %w"
		switch fls % 4 {
		case 0:
			return func() error {
				return fmt.Errorf(wfmt, prev())
			}
		case 1:
			return func() error {
				return fmt.Errorf(wfmt, prev())
			}
		case 2:
			return func() error {
				return fmt.Errorf(wfmt, prev())
			}
		case 3:
			return func() error {
				return fmt.Errorf(wfmt, prev())
			}
		default:
			return func() error {
				return fmt.Errorf(wfmt, prev())
			}
		}
	}

	erf := func() error {
		return errPropSentinel
	}
	for i := range chnsz {
		erf = makeNextFunc(i, erf)
	}

	return erf
}

func makeRaisedPropChain(strsz int, chnsz int) errfunc {
	makeNextFunc := func(fls int, prev errfunc) errfunc {
		msg := fmt.Sprintf("[%d]: %s", fls, rndString(strsz))
		switch fls % 4 {
		case 0:
			return func() error {
				return Trace(prev(), msg)
			}
		case 1:
			return func() error {
				return Trace(prev(), msg)
			}
		case 2:
			return func() error {
				return Trace(prev(), msg)
			}
		case 3:
			return func() error {
				return Trace(prev(), msg)
			}
		default:
			return func() error {
				return Trace(prev(), msg)
			}
		}
	}

	erf := func() error {
		return errPropSentinel
	}
	for i := range chnsz {
		erf = makeNextFunc(i, erf)
	}

	return erf
}

func makeRaisedPCCachePropChain(strsz int, chnsz int) errfunc {
	makeNextFunc := func(fls int, prev errfunc) errfunc {
		msg := fmt.Sprintf("[%d]: %s", fls, rndString(strsz))
		switch fls % 4 {
		case 0:
			return func() error {
				return TraceAt(10, prev(), msg)
			}
		case 1:
			return func() error {
				return TraceAt(20, prev(), msg)
			}
		case 2:
			return func() error {
				return TraceAt(30, prev(), msg)
			}
		case 3:
			return func() error {
				return TraceAt(40, prev(), msg)
			}
		default:
			return func() error {
				return TraceAt(0, prev(), msg)
			}
		}
	}

	erf := func() error {
		return errPropSentinel
	}
	for i := range chnsz {
		erf = makeNextFunc(i, erf)
	}

	return erf
}

func TestDesign_propRaised(t *testing.T) {
	errFunc := makeRaisedPropChain(32, 12)
	err := errFunc()
	t.Logf("err -> %v", err)
	t.Logf("err %%s \n%s", err)
	t.Logf("err %%v \n%v", err)
	t.Logf("err %%+v \n%+v", err)
}

func TestDesign_propRaised02(t *testing.T) {
	errFunc := makeRaisedPropChain(32, 16)
	err := errFunc()
	t.Logf("err1 -> \n%v", err)
	err = errFunc()
	t.Logf("err2 -> \n%v", err)
}

// ====================================================================================
// Evaluating Cause determination performance

func BenchmarkDesign_unwrap(b *testing.B) {
	var sentinels []error
	for i := range 8 {
		sentinels = append(sentinels, errors.New(fmt.Sprintf("ERROR(%d)", i)))
	}
	sentinel := errors.New("ERROR(111): runtime sentinel")
	sentinels = append(sentinels, sentinel)
	err := fmt.Errorf("[0] %w", sentinel)
	for b.Loop() {
		switch v := err.(type) {
		case interface{ Unwrap() error }:
			var found bool
			ec := v.Unwrap()
			for _, s := range sentinels {
				if errors.Is(ec, s) {
					found = true
					break
				}
			}
			if !found {
				panic("can not match sentinel")
			}
		default:
			panic("not an Unwrapper")
		}
	}
}

func BenchmarkDesign_unwrapt1l1(b *testing.B) {
	err := errors.New("ERROR(111): runtime sentinel")
	dst := [1]error{}
	for b.Loop() {
		_, _ = extractTerminals(err, dst[:0])
	}
}

func BenchmarkDesign_unwrapt1l8(b *testing.B) {
	err := makeWrappedError(
		inerr{8, errors.New("ERROR(111): runtime sentinel")},
	)
	dst := [1]error{}
	for b.Loop() {
		_, _ = extractTerminals(err, dst[:0])
	}
}

func TestDesign_unwrap01(t *testing.T) {
	err := makeWrappedError(
		inerr{4, errors.New("ERROR(112): something weird happened...")},
		inerr{8, errors.New("ERROR(224): airplane crashed")},
	)
	t.Logf("err -> %+v", err)
	terms, lvl := extractTerminals(err, nil)
	t.Logf("Wrap level %d | [%d]terms -> %v", lvl, len(terms), terms)

}

func TestDesign_unwrap02(t *testing.T) {
	err := fmt.Errorf("[1] %w", errors.New("ERROR(111)"))
	err = fmt.Errorf("[0] %w %w", err, errors.New("ERROR(222)"))
	t.Logf("err -> %+v", err)
	terms, lvl := extractTerminals(err, nil)
	t.Logf("Wrap level %d | [%d]terms -> %v", lvl, len(terms), terms)
}

func makeWrappedError(errs ...inerr) error {
	errIdx := make(map[int][]error)
	var errlist []error
	var lvl, maxlvl int
	for _, ine := range errs {
		lvl = int(ine.lvl)
		if lvl > maxlvl {
			maxlvl = lvl
		}
		errlist, _ = errIdx[lvl]
		errlist = append(errlist, ine.err)
		errIdx[lvl] = errlist
	}

	var ok bool
	var wfmt string
	var err error
	errors := make([]error, 4)
	args := make([]any, 5)
	for i := maxlvl; i >= 0; i -= 1 {
		errors = errors[:0]
		if nil != err {
			errors = append(errors, err)
		}
		errlist, ok = errIdx[i]
		if ok {
			errors = append(errors, errlist...)
		}
		if len(errors) > 0 {
			args = args[:0]
			args = append(args, i)
			for _, e := range errors {
				args = append(args, e)
			}
			wfmt = "[%d]" + strings.Repeat(" %w", len(errors))
			err = fmt.Errorf(wfmt, args...)
		}
	}

	return err

}

func extractTerminals(err error, dst []error) ([]error, int) {
	var lvl int
	var e, en error
	var errlist, errnext, ens []error
	var ers1, ers2 [16]error
	errlist = ers1[:0]
	errnext = ers2[:0]
	errlist = append(errlist, err)
	for {
		lvl += 1
		for _, e = range errlist {
			switch v := e.(type) {
			case interface{ Unwrap() error }:
				en = v.Unwrap()
				if nil != en {
					errnext = append(errnext, en)
				}
			case interface{ Unwrap() []error }:
				ens = v.Unwrap()
				for _, en = range ens {
					errnext = append(errnext, en)
				}
			default:
				dst = append(dst, e)
			}
		}
		if 0 == len(errnext) {
			break
		}
		ens = errlist[:0] // tmp reference for swap
		errlist = errnext
		errnext = ens
	}

	return dst, lvl
}

type inerr struct {
	lvl uint8
	err error
}

// ====================================================================================
// string & array of strings comparison performance
// interning overhead vs gain ?
//
// main take of those benchmarks is that direct comparison of [8]string is pretty good.
// eg see BenchmarkDesign_CmpA8S128almost
// this validates using simple RingBuffer storage for Error() & Trace() caching.

func BenchmarkDesign_CmpS32distinct(b *testing.B) {
	s := rndString(32)
	s1 := fmt.Sprintf("0%s", s)
	s2 := fmt.Sprintf("1%s", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpS32almost(b *testing.B) {
	s := rndString(32)
	s1 := fmt.Sprintf("%s0", s)
	s2 := fmt.Sprintf("%s1", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpS128distinct(b *testing.B) {
	s := rndString(128)
	s1 := fmt.Sprintf("0%s", s)
	s2 := fmt.Sprintf("1%s", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpS128almost(b *testing.B) {
	s := rndString(128)
	s1 := fmt.Sprintf("%s0", s)
	s2 := fmt.Sprintf("%s1", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpS512distinct(b *testing.B) {
	s := rndString(512)
	s1 := fmt.Sprintf("0%s", s)
	s2 := fmt.Sprintf("1%s", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpS512almost(b *testing.B) {
	s := rndString(512)
	s1 := fmt.Sprintf("%s0", s)
	s2 := fmt.Sprintf("%s1", s)

	for b.Loop() {
		if s1 == s2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S32distinct(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(32)
		a2[i] = a1[i]
	}
	s := a1[0]
	a1[0] = fmt.Sprintf("0%s", s)
	a2[0] = fmt.Sprintf("1%s", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S32almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(32)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpInternA8S32almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(32)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	a1h := unique.Make(a1)

	for b.Loop() {
		a2h := unique.Make(a2)
		if a1h == a2h {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S128distinct(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(128)
		a2[i] = a1[i]
	}
	s := a1[0]
	a1[0] = fmt.Sprintf("0%s", s)
	a2[0] = fmt.Sprintf("1%s", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S128almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(128)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpInternA8S128almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(128)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	a1h := unique.Make(a1)

	for b.Loop() {
		a2h := unique.Make(a2)
		if a1h == a2h {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S512distinct(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(512)
		a2[i] = a1[i]
	}
	s := a1[0]
	a1[0] = fmt.Sprintf("0%s", s)
	a2[0] = fmt.Sprintf("1%s", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpA8S512almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(512)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	for b.Loop() {
		if a1 == a2 {
			panic("unreachable")
		}
	}
}

func BenchmarkDesign_CmpInternA8S512almost(b *testing.B) {
	a1 := [8]string{}
	a2 := [8]string{}
	for i := range 8 {
		a1[i] = rndString(512)
		a2[i] = a1[i]
	}
	s := a1[7]
	a1[7] = fmt.Sprintf("%s0", s)
	a2[7] = fmt.Sprintf("%s1", s)

	a1h := unique.Make(a1)

	for b.Loop() {
		a2h := unique.Make(a2)
		if a1h == a2h {
			panic("unreachable")
		}
	}
}

// ====================================================================================
// Utilities

var rawB64 = base64.StdEncoding.WithPadding(base64.NoPadding)

// rndString returns a base64 string encoding sz random bytes.
func rndString(sz int) string {
	buf := make([]byte, sz)
	rand.Read(buf)

	return rawB64.EncodeToString(buf)
}

func TestDesign_RndString(t *testing.T) {
	t.Logf("rndString(32) -> %s", rndString(32))
	t.Logf("rndString(64) -> %s", rndString(64))
	t.Logf("rndString(128) -> %s", rndString(128))
}
