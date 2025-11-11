package main

import (
	"fmt"
	"regexp"
)

// checkMacro validates that a macro name is a valid Go identifier and not a reserved word.
// It returns an error if the name is a keyword, predeclared identifier, or invalid format.
func checkMacro(name string) error {
	// Check ASCII identifier pattern
	matched, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, name)
	if !matched {
		return fmt.Errorf("macro name must start with a letter or underscore, followed by letters, digits, or underscores")
	}

	// Check for keywords
	_, isKeyword := keywords[name]
	if isKeyword {
		return fmt.Errorf("macro name cannot be a Go keyword")
	}

	// Check for predeclared identifiers
	_, isPredeclared := predeclared[name]
	if isPredeclared {
		return fmt.Errorf("macro name cannot be a predeclared Go identifier")
	}

	return nil
}

// keywords maps go language keywords to prevent their use as macro names.
var keywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
}

// predeclared maps go language predeclared identifiers to prevent their use as macro names.
var predeclared = map[string]bool{
	// Types
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,

	// Constants
	"true": true, "false": true, "iota": true,

	// Zero value
	"nil": true,

	// Functions
	"append": true, "cap": true, "close": true, "complex": true, "copy": true,
	"delete": true, "imag": true, "len": true, "make": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true, "recover": true,
}
