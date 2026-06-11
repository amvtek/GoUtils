# GoUtils

packages and tools useful in golang projects.

## pkg/raised
Provides a complete error tracing & keying solution for golang projects.

raised traces are memory efficient, they require a single allocation regardless of the
number of intermediary steps.

## cmd/unik
Source code preprocessor that replaces a configurable pattern (aka macro...) by unique
integer values.

Used to define cache keys, unique error codes...
