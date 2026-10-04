// SPDX-License-Identifier: Apache-2.0

package gonsole

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Env reads settings under one prefix.
type Env struct {
	// Prefix starts every setting name, such as MYAPP_.
	Prefix string
	// Getenv reads one variable, nil reading every variable as empty.
	Getenv func(string) string
}

// Key returns the full name of the setting called name.
func (e Env) Key(name string) string {
	return e.Prefix + name
}

// Value returns the setting's value with surrounding spaces trimmed, empty when it is unset.
func (e Env) Value(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv(e.Key(name)))
}

// Required returns the setting's value, an error naming it when it is empty.
func (e Env) Required(name string) (string, error) {
	value := e.Value(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", e.Key(name))
	}
	return value, nil
}

// Duration returns the setting as a duration above zero, the fallback when it is empty.
func (e Env) Duration(name string, fallback time.Duration, bounds ...Bound) (time.Duration, error) {
	return Parse(e, name, fallback, narrow(math.MaxInt64, bounds).duration)
}

// Count returns the setting as a whole number above zero, the fallback when it is empty.
func (e Env) Count(name string, fallback int, bounds ...Bound) (int, error) {
	return Parse(e, name, fallback, narrow(math.MaxInt, bounds).whole)
}

// Counts returns the setting as rising whole numbers above zero split by commas, the fallback when it is empty.
func (e Env) Counts(name string, fallback []int, bounds ...Bound) ([]int, error) {
	return Parse(e, name, fallback, narrow(math.MaxInt, bounds).counts)
}

// Flag returns the setting as true or false, the fallback when it is empty.
func (e Env) Flag(name string, fallback bool) (bool, error) {
	return Parse(e, name, fallback, func(value string) (bool, error) {
		read, err := strconv.ParseBool(value)
		if err != nil {
			return false, complaint("must be true or false", value)
		}
		return read, nil
	})
}

// Within returns the settings under the prefix followed by more.
func (e Env) Within(more string) Env {
	return Env{Prefix: e.Prefix + more, Getenv: e.Getenv}
}

// Parse returns the setting read by parse, the fallback when it is empty, any error naming the setting.
func Parse[T any](e Env, name string, fallback T, parse func(string) (T, error)) (T, error) {
	value := e.Value(name)
	if value == "" {
		return fallback, nil
	}
	read, err := parse(value)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s: %w", e.Key(name), err)
	}
	return read, nil
}

// complaint returns the error saying what a setting must be and the value it holds.
func complaint(must, value string) error {
	return fmt.Errorf("%s, got %q", must, value)
}

// durationText prints a count of nanoseconds as a duration.
func durationText(n int64) string {
	return time.Duration(n).String()
}

// wholeText prints a whole number in base ten.
func wholeText(n int64) string {
	return strconv.FormatInt(n, 10)
}

// limits are the values a Count, Counts or Duration setting accepts.
type limits struct {
	highest   int64
	allowZero bool
	wholeMs   bool
	fewest    int
	most      int
}

// Bound narrows what a Count, Counts or Duration setting accepts, each reader ignoring a bound made for another.
type Bound func(*limits)

// AtMost refuses a value above highest.
func AtMost(highest int64) Bound {
	return func(l *limits) { l.highest = min(l.highest, highest) }
}

// AllowZero accepts zero beside the values above it.
func AllowZero() Bound {
	return func(l *limits) { l.allowZero = true }
}

// WholeMilliseconds refuses a Duration setting that is not a whole number of milliseconds.
func WholeMilliseconds() Bound {
	return func(l *limits) { l.wholeMs = true }
}

// Entries refuses a Counts setting that lists fewer than fewest or more than most numbers.
func Entries(fewest, most int) Bound {
	return func(l *limits) {
		l.fewest = max(l.fewest, fewest)
		l.most = min(l.most, most)
	}
}

// narrow returns the limits of the values above zero up to top, as bounds change them.
func narrow(top int64, bounds []Bound) limits {
	within := limits{highest: top, most: math.MaxInt}
	for _, bound := range bounds {
		bound(&within)
	}
	return within
}

// duration reads value as a duration within l.
func (l limits) duration(value string) (time.Duration, error) {
	read, err := time.ParseDuration(value)
	if err != nil {
		return 0, complaint("must be a duration like 30s", value)
	}
	if err = l.judge(int64(read), false, value, durationText); err != nil {
		return 0, err
	}
	if l.wholeMs && read%time.Millisecond != 0 {
		return 0, complaint("must be a whole number of milliseconds", value)
	}
	return read, nil
}

// whole reads value as a whole number within l.
func (l limits) whole(value string) (int, error) {
	read, err := strconv.ParseInt(value, 10, 0)
	overflowed := errors.Is(err, strconv.ErrRange)
	if err != nil && !overflowed {
		return 0, complaint("must be a whole number", value)
	}
	return int(read), l.judge(read, overflowed, value, wholeText)
}

// counts reads value as whole numbers within l split by commas, each above the one before.
func (l limits) counts(value string) ([]int, error) {
	entries := strings.Split(value, ",")
	read := make([]int, 0, len(entries))
	for _, entry := range entries {
		count, err := l.whole(strings.TrimSpace(entry))
		if err != nil {
			return nil, fmt.Errorf("%w in %q", err, value)
		}
		if len(read) > 0 && count <= read[len(read)-1] {
			return nil, complaint("must list each number once from the smallest up", value)
		}
		read = append(read, count)
	}
	if len(read) < l.fewest || len(read) > l.most {
		return nil, complaint(l.span(), value)
	}
	return read, nil
}

// span says how many numbers a Counts setting within l must list.
func (l limits) span() string {
	if l.most == math.MaxInt {
		return fmt.Sprintf("must list at least %d numbers", l.fewest)
	}
	return fmt.Sprintf("must list from %d to %d numbers", l.fewest, l.most)
}

// judge refuses a value n that falls outside l or that overflowed past the ceiling.
func (l limits) judge(n int64, overflowed bool, value string, show func(int64) string) error {
	switch {
	case n < 0 && l.allowZero:
		return complaint("must not be negative", value)
	case n <= 0 && !l.allowZero:
		return complaint("must stand above zero", value)
	case n > l.highest || overflowed:
		return complaint("must stand at or below "+show(l.highest), value)
	}
	return nil
}

// settings returns the program's settings with a reader that is never nil.
func (r *runner) settings() Env {
	env := r.program.Env
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	return env
}
