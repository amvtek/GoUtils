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
