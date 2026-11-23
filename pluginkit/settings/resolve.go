// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"fmt"
	"strings"
)

// Sources holds what an application found for a schema: environment values by variable name and saved values by key.
type Sources struct {
	Env   map[string]string
	Saved map[string]string
}

// RejectedSavedValue is a saved value its field no longer takes, so the default applies.
type RejectedSavedValue struct {
	Key string
	Err error
}

// Resolve works out each field from the environment, then the saved value, then the default.
func (s Schema) Resolve(found Sources) (Values, []RejectedSavedValue, error) {
	values := make(Values, len(s.Fields))
	var rejected []RejectedSavedValue
	for _, field := range s.Fields {
		value, report, err := field.resolve(found)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", field.Env, err)
		}
		if report != nil {
			rejected = append(rejected, *report)
		}
		values[field.Key] = value
	}
	return values, rejected, nil
}

// resolve returns the field's value from found, with the report of a saved value it passed over for the default.
func (f Field) resolve(found Sources) (Value, *RejectedSavedValue, error) {
	parse, err := parserOf(f.Type)
	if err != nil {
		return Value{}, nil, err
	}
	if env := found.Env[f.Env]; strings.TrimSpace(env) != "" {
		value, err := parse(f, env)
		value.Source = SourceEnvironment
		return value, nil, err
	}
	saved, isSaved := found.Saved[f.Key]
	if !isSaved {
		value, err := f.byDefault(parse)
		return value, nil, err
	}
	value, rejection := f.fromSaved(parse, saved)
	if rejection == nil {
		return value, nil, nil
	}
	value, err = f.byDefault(parse)
	return value, &RejectedSavedValue{Key: f.Key, Err: rejection}, err
}

// fromSaved returns the saved value read by parse, or why the field rejects it, a secret rejecting any saved value.
func (f Field) fromSaved(parse parser, saved string) (Value, error) {
	if f.Type == TypeSecret {
		return Value{}, &ValueError{Reason: ReasonEnvironmentOnly, text: "a secret comes from the environment only"}
	}
	value, err := parse(f, saved)
	value.Source = SourceSaved
	return value, err
}

// byDefault returns the field's default read by parse, the zero value of its type when it has none or is a secret.
func (f Field) byDefault(parse parser) (Value, error) {
	text := strings.TrimSpace(f.Default)
	if text == "" || f.Type == TypeSecret {
		return Value{Type: f.Type, Source: SourceDefault}, nil
	}
	value, err := parse(f, text)
	value.Source = SourceDefault
	return value, err
}
