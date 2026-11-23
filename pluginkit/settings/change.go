// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

const (
	// KindUnknown is the kind of a change to a key the schema does not declare.
	KindUnknown = "unknown"
	// KindLocked is the kind of a change to a field whose variable holds a value.
	KindLocked = "locked"
	// KindInvalid is the kind of a change to a value its field rejects.
	KindInvalid = "invalid"
)

// ChangeError is the first problem a change holds, with the bound the check used in Min or Max.
type ChangeError struct {
	Kind    string
	Key     string
	Env     string
	Reason  string
	Min     *int
	Max     *int
	Options []string
}

// Error returns the key and its problem.
func (e *ChangeError) Error() string {
	switch e.Kind {
	case KindUnknown:
		return e.Key + ": no setting has this key"
	case KindLocked:
		return e.Key + ": locked by " + e.Env
	}
	return e.Key + ": value rejected as " + e.Reason
}

// CheckChange checks a change against the schema and the environment, answering the first problem it holds.
func (s Schema) CheckChange(change, env map[string]string) error {
	if key, found := s.undeclared(change); found {
		return &ChangeError{Kind: KindUnknown, Key: key}
	}
	for _, field := range s.Fields {
		value, changed := change[field.Key]
		if !changed {
			continue
		}
		if err := field.checkChange(value, env[field.Env]); err != nil {
			return err
		}
	}
	return nil
}

// undeclared returns the first key of change in sorted order that the schema does not declare, and whether one is.
func (s Schema) undeclared(change map[string]string) (string, bool) {
	for _, key := range slices.Sorted(maps.Keys(change)) {
		if !s.declares(key) {
			return key, true
		}
	}
	return "", false
}

// declares reports whether the schema holds a field with key.
func (s Schema) declares(key string) bool {
	return slices.ContainsFunc(s.Fields, func(field Field) bool { return field.Key == key })
}

// checkChange answers the problem of value as a change to the field while its variable holds held, nil for none.
func (f Field) checkChange(value, held string) error {
	if strings.TrimSpace(held) != "" {
		return &ChangeError{Kind: KindLocked, Key: f.Key, Env: f.Env}
	}
	if f.Type == TypeSecret {
		return f.invalid(ReasonEnvironmentOnly)
	}
	parse, err := parserOf(f.Type)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Key, err)
	}
	_, err = parse(f, value)
	var rejection *ValueError
	if errors.As(err, &rejection) {
		return f.invalid(rejection.Reason)
	}
	return err
}

// invalid returns the change error of a value the field rejects for reason, with the bound or options it names.
func (f Field) invalid(reason string) *ChangeError {
	problem := &ChangeError{Kind: KindInvalid, Key: f.Key, Env: f.Env, Reason: reason}
	switch reason {
	case ReasonBelowMin:
		floor := orElse(f.Min, math.MinInt)
		problem.Min = &floor
	case ReasonAboveMax:
		ceiling := orElse(f.Max, math.MaxInt)
		problem.Max = &ceiling
	case ReasonNotOption:
		problem.Options = slices.Clone(f.Options)
	}
	return problem
}
