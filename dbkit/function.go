// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"database/sql/driver"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Function is one Go function an engine registers for SQL to call.
type Function struct {
	// Name is the name SQL calls the function by, its ASCII letters matched in any case and every other letter exactly.
	Name string
	// Args is the count of arguments the function takes, or -1 for any count.
	Args int
	// Deterministic reports that the same arguments always give the same result.
	Deterministic bool
	// Call computes the result from the arguments.
	Call func(args []driver.Value) (driver.Value, error)
}

// FunctionList is a checked list of functions with no name repeated in any case.
type FunctionList struct {
	// fns holds the functions in the order given.
	fns []Function
}

// NewFunctionList returns a list of fns, or an error naming the first function it refuses.
func NewFunctionList(fns ...Function) (*FunctionList, error) {
	seen := make(map[string]string, len(fns))
	for i, fn := range fns {
		if err := checkFunction(i, fn); err != nil {
			return nil, err
		}
		key := foldString(fn.Name)
		if first, ok := seen[key]; ok {
			return nil, fmt.Errorf("dbkit: function %q repeats the name of function %q", fn.Name, first)
		}
		seen[key] = fn.Name
	}
	return &FunctionList{fns: slices.Clone(fns)}, nil
}

// checkFunction returns the error for the function at index i, or nil when it is accepted.
func checkFunction(i int, fn Function) error {
	switch {
	case fn.Name == "":
		return fmt.Errorf("dbkit: function %d has an empty name", i)
	case fn.Args < -1:
		return fmt.Errorf("dbkit: function %q takes Args %d, want -1 for any count or 0 and more", fn.Name, fn.Args)
	case fn.Call == nil:
		return fmt.Errorf("dbkit: function %q has a nil Call", fn.Name)
	}
	return nil
}

// All returns a copy of the functions in the list, or nil for a nil list.
func (l *FunctionList) All() []Function {
	if l == nil {
		return nil
	}
	return slices.Clone(l.fns)
}

// CaseFold returns casefold, a deterministic function of one argument that folds text by Unicode simple folding.
func CaseFold() Function {
	return Function{Name: "casefold", Args: 1, Deterministic: true, Call: caseFold}
}

// caseFold folds a text or blob argument into a string and returns any other value unchanged.
func caseFold(args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("dbkit: casefold takes one argument, got %d", len(args))
	}
	switch v := args[0].(type) {
	case string:
		return foldString(v), nil
	case []byte:
		return foldString(string(v)), nil
	default:
		return v, nil
	}
}

// foldString maps every rune of s through foldRune and keeps every byte of invalid UTF-8 as it is.
func foldString(s string) string {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if foldRune(r) != r {
			return foldFrom(s, i)
		}
		i += size
	}
	return s
}

// foldFrom returns s with every rune from byte i on mapped through foldRune and every unchanged byte kept.
func foldFrom(s string, i int) string {
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if folded := foldRune(r); folded != r {
			b.WriteRune(folded)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// foldRune returns the smallest rune in the simple folding orbit of r, or its lower case when the orbit holds that.
func foldRune(r rune) rune {
	smallest := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		smallest = min(smallest, f)
	}
	if lower := unicode.ToLower(smallest); inOrbit(r, lower) {
		return lower
	}
	return smallest
}

// inOrbit reports whether c is in the simple folding orbit of r.
func inOrbit(r, c rune) bool {
	if c == r {
		return true
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f == c {
			return true
		}
	}
	return false
}
