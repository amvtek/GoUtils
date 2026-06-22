// Package raised provides structured error propagation, tracing, and stable
// error identity for Go programs.
//
// # Overview
//
// Standard Go errors carry no information about how they propagated through
// the call stack. raised addresses this by wrapping errors in a trace that
// records each propagation step — the call site file, line, and an optional
// message — as the error travels up the call stack.
//
// raised also provides typed sentinel errors scoped to a package, and a
// stable hashing mechanism that identifies errors by their propagation path
// and root cause, independently of any dynamic context embedded in error
// messages.
//
// # Sentinels
//
// Sentinel errors are package-level error constants declared with NewSentinel
// or NewSentinelError. The phantom type parameter T scopes the sentinel to the
// declaring package, preventing accidental errors.Is matches across packages.
// An optional ERROR(n) prefix embeds a numeric code for lightweight
// classification:
//
//	type pkg struct{}
//
//	var (
//	    ErrNotFound   = raised.NewSentinelError[pkg]("ERROR(1) not found")
//	    ErrBadRequest = raised.NewSentinelError[pkg]("ERROR(2) bad request")
//	)
//
// # Tracing
//
// Trace wraps any error in a propagation trace. Each call records
// the call site and an optional message. If the error is already a raised
// Error it is extended in place; otherwise a new trace is created with the
// error as its root cause:
//
//	func readConfig(path string) error {
//	    f, err := os.Open(path)
//	    if err != nil {
//	        return raised.Trace(err, "open config")
//	    }
//	    // ...
//	}
//
//	func loadApp() error {
//	    if err := readConfig("app.yaml"); err != nil {
//	        return raised.Trace(err, "load app")
//	    }
//	    // ...
//	}
//
// The full traceback is available via the Trace() method or %+v formatting:
//
//	if err := loadApp(); err != nil {
//	    fmt.Printf("%+v\n", err)
//	}
//
// # Classification
//
// When a package receives a foreign error it can assert its own sentinel
// identity without changing the underlying cause, using Classify:
//
//	func fetchUser(id string) error {
//	    user, err := db.Get(id)
//	    if err != nil {
//	        err = raised.Trace(err, "fetch user")
//	        err.Classify(ErrNotFound)
//	        return err
//	    }
//	    // ...
//	}
//
//	if errors.Is(err, ErrNotFound) {
//	    // true, regardless of the underlying db error
//	}
//
// # Error identity and keying
//
// An ErrorKeyer computes a stable ErrorKey for a raised Error, derived from its
// propagation path and terminal root cause. Two errors sharing the same
// ErrorKey represent the same problem: identical code path and equivalent
// root cause. This is useful for error aggregation, deduplication, and
// monitoring.
//
// An ErrorKeyer is scoped to the sentinel family T, consistent with the phantom
// type used for sentinel declaration:
//
//	     type pkg struct {}
//
//		var Keyer, _ = raised.NewErrorKeyer[pkg](nil)
//
//		func handle(err error) {
//		    key, ok := Keyer.Key(err)
//		    if ok {
//		        monitor.Record(key)
//		    }
//		}
//
// The ErrorKey is stable across process restarts and hosts as long as the
// source code has not changed — it is derived from file/line strings rather
// than runtime memory addresses.
package raised
