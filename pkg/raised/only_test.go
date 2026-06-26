package raised

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

type errfunc = func() error

var rawB64 = base64.StdEncoding.WithPadding(base64.NoPadding)

// makeRaisedPropChain returns an errfunc which when called propagates the
// cause error accross chnsz locations. Each Trace message holds strsz random
// bytes encoded to base64.
func makeRaisedPropChain(strsz int, chnsz int, cause error) errfunc {
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
		return cause
	}
	for i := range chnsz {
		erf = makeNextFunc(i, erf)
	}

	return erf
}

// rndString returns a base64 string encoding sz random bytes.
func rndString(sz int) string {
	buf := make([]byte, sz)
	rand.Read(buf)

	return rawB64.EncodeToString(buf)
}

// ====================================================================================
// code below was part of package public interface

// TraceAt behaves like Trace but caches the call site PC using flk as a
// lookup key, avoiding repeated runtime.Callers calls on hot paths.
// flk must be a non-zero integer constant unique within the calling package.
// If args are provided, msg is used as a format string.
// Returns nil if err is nil.
func TraceAt[K ~int](flk K, err error, msg string, args ...any) Error {
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
	addCallerInfo(rv, flk, msg, 1)
	return rv
}
