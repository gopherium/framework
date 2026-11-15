// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// parser reads one value of a field's type.
type parser func(field Field, value string) (Value, error)

// types lists the seven setting types in the order an error names them.
var types = []string{TypeText, TypeCount, TypeFlag, TypeDuration, TypeChoice, TypeSecret, TypeLines}

// parsers holds the parser of each setting type.
var parsers = map[string]parser{
	TypeText:     parseText,
	TypeCount:    parseCount,
	TypeFlag:     parseFlag,
	TypeDuration: parseDuration,
	TypeChoice:   parseChoice,
	TypeSecret:   parseSecret,
	TypeLines:    parseLines,
}

// parserOf returns the parser of the setting type kind, an error naming the seven types when kind is none of them.
func parserOf(kind string) (parser, error) {
	parse, known := parsers[kind]
	if !known {
		return nil, fmt.Errorf("type %q is not one of %s", kind, strings.Join(types, ", "))
	}
	return parse, nil
}

// complaint returns the error saying what a value must be and the value it is.
func complaint(must, value string) error {
	return fmt.Errorf("%s, got %q", must, value)
}

// parseText reads value as text with its surrounding spaces trimmed.
func parseText(_ Field, value string) (Value, error) {
	return Value{Type: TypeText, Text: strings.TrimSpace(value)}, nil
}

// parseCount reads value as a whole number within the field's min and max.
func parseCount(field Field, value string) (Value, error) {
	value = strings.TrimSpace(value)
	read, err := strconv.ParseInt(value, 10, 0)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return Value{}, complaint("must be a whole number", value)
	}
	if rule := beyond(field, read, err != nil); rule != "" {
		return Value{}, complaint(rule, value)
	}
	return Value{Type: TypeCount, Count: int(read)}, nil
}

// beyond returns the bound of field that read breaks, empty when read stands within them.
func beyond(field Field, read int64, overflowed bool) string {
	floor, ceiling := orElse(field.Min, math.MinInt), orElse(field.Max, math.MaxInt)
	switch {
	case read < int64(floor), overflowed && read == math.MinInt:
		return "must stand at or above " + strconv.Itoa(floor)
	case read > int64(ceiling), overflowed:
		return "must stand at or below " + strconv.Itoa(ceiling)
	}
	return ""
}

// orElse returns the number n points to, fallback when n is nil.
func orElse(n *int, fallback int) int {
	if n == nil {
		return fallback
	}
	return *n
}

// parseFlag reads value as true or false.
func parseFlag(_ Field, value string) (Value, error) {
	value = strings.TrimSpace(value)
	read, err := strconv.ParseBool(value)
	if err != nil {
		return Value{}, complaint("must be true or false", value)
	}
	return Value{Type: TypeFlag, Flag: read}, nil
}

// parseDuration reads value as a duration above zero.
func parseDuration(_ Field, value string) (Value, error) {
	value = strings.TrimSpace(value)
	read, err := time.ParseDuration(value)
	if err != nil {
		return Value{}, complaint("must be a duration like 30s", value)
	}
	if read <= 0 {
		return Value{}, complaint("must stand above zero", value)
	}
	return Value{Type: TypeDuration, Duration: read}, nil
}

// parseChoice reads value as one of the field's options.
func parseChoice(field Field, value string) (Value, error) {
	value = strings.TrimSpace(value)
	if !slices.Contains(field.Options, value) {
		return Value{}, complaint("must be one of "+strings.Join(field.Options, ", "), value)
	}
	return Value{Type: TypeChoice, Text: value}, nil
}

// parseSecret takes value as written and keeps it hidden.
func parseSecret(_ Field, value string) (Value, error) {
	return Value{Type: TypeSecret, Secret: hide(value)}, nil
}

// parseLines reads value as a comma list, each item trimmed and the empty items dropped.
func parseLines(_ Field, value string) (Value, error) {
	var lines []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			lines = append(lines, item)
		}
	}
	return Value{Type: TypeLines, Lines: lines}, nil
}
