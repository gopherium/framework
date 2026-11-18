// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"errors"
	"strconv"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// plainEscape and unitEscape are the lengths of an escape such as \n and of one naming a UTF-16 code unit.
const (
	plainEscape = 2
	unitEscape  = 6
)

// unitPrefix starts an escape naming a UTF-16 code unit by four hex digits.
var unitPrefix = []byte(`\u`)

// checkText rejects data, JSON json.Valid accepted, when it is not valid UTF-8 or escapes a surrogate with no pair.
func checkText(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("the manifest holds text that is not valid UTF-8")
	}
	rest := data
	for at := bytes.IndexByte(rest, '\\'); at >= 0; at = bytes.IndexByte(rest, '\\') {
		size, paired := escapeAt(rest[at:])
		if !paired {
			return errors.New("the manifest holds an escaped surrogate with no pair")
		}
		rest = rest[at+size:]
	}
	return nil
}

// escapeAt returns the length of the escape at the start of b, and whether each surrogate it names sits in a pair.
func escapeAt(b []byte) (int, bool) {
	first := unitAt(b)
	switch {
	case first < 0:
		return plainEscape, true
	case !utf16.IsSurrogate(first):
		return unitEscape, true
	}
	return 2 * unitEscape, utf16.DecodeRune(first, unitAt(b[unitEscape:])) != unicode.ReplacementChar
}

// unitAt returns the UTF-16 code unit an escape at the start of b names, or -1 when b starts with no such escape.
func unitAt(b []byte) rune {
	digits, named := bytes.CutPrefix(b, unitPrefix)
	if !named {
		return -1
	}
	unit, _ := strconv.ParseUint(string(digits[:unitEscape-len(unitPrefix)]), 16, 16)
	return rune(unit)
}
