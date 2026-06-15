package raised

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	codeRe        = regexp.MustCompile(`^\s*ERROR\((-?[0-9][0-9_]*)\)`)
	remUnderscore = strings.NewReplacer("_", "")
)

// SentinelError is the interface implemented by package-level error values.
// It extends error with a numeric Code, enabling lightweight classification
// and comparison without relying on message strings.
type SentinelError interface {
	error

	// Code returns the numeric identifier embedded in the sentinel message
	// via the ERROR(n) prefix, or 0 if no such prefix was present.
	Code() int

	// Fingerprint returns a representation of the error suitable for hashing.
	Fingerprint() string

	isSentinel() // unexported, prevent external implementation
}

// Sentinel is a generic error value intended for package-level declaration.
// The type parameter T is a phantom type that lets each package define
// a distinct sentinel family, preventing accidental cross-package errors.Is
// matches between sentinels that share the same message and code.
type Sentinel[T any] struct {
	code  int
	msg   string
	clamp int
}

// NewSentinel creates a SentinelError using the default phantom type.
// Prefer NewSentinelError when the calling package wants a distinct sentinel family.
// Should only be called at package initialisation time (var declarations).
func NewSentinel(msg string) SentinelError {
	return NewSentinelError[pkg](msg)
}

// NewSentinelError creates a *Sentinel[T] for package-level error declaration.
// If msg begins with ERROR(n), the integer n is extracted as the sentinel's Code
// and the prefix is normalised, stripping any underscore separators from n.
// Should only be called at package initialisation time (var declarations).
func NewSentinelError[T any](msg string) *Sentinel[T] {
	var code int
	msg = strings.TrimSpace(msg)
	smt := codeRe.FindStringSubmatch(msg)
	if 2 == len(smt) {
		num := remUnderscore.Replace(smt[1])
		code, _ = strconv.Atoi(num)
		msg = codeRe.ReplaceAllString(msg, fmt.Sprintf("ERROR(%s)", num))
	}
	rv := Sentinel[T]{code: code, clamp: len(msg)}
	rv.msg = fmt.Sprintf("%s::%T", msg, rv) // append sentinel type to error msg
	return &rv
}

func (self *Sentinel[T]) Error() string {
	if (self.clamp > 0) && (self.clamp < len(self.msg)) {
		return self.msg[0:self.clamp]
	} else {
		return self.msg
	}
}

func (self *Sentinel[T]) Code() int {
	return self.code
}

func (self *Sentinel[T]) Fingerprint() string {
	return self.msg
}

func (self *Sentinel[T]) isSentinel() {}

// phantom type used to instantiate default Sentinel
type pkg struct{}

var _ SentinelError = &Sentinel[pkg]{}
