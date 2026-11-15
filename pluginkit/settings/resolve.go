// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"fmt"
	"strings"
)

// Resolve works out each field's value from found, the environment values by variable name, then from its default.
func (s Schema) Resolve(found map[string]string) (Values, error) {
	values := make(Values, len(s.Fields))
	for _, field := range s.Fields {
		value, err := field.read(field.pick(found[field.Env]))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.Env, err)
		}
		values[field.Key] = value
	}
	return values, nil
}

// pick returns the environment's value of the field, its default when the value is blank, a secret taking no default.
func (f Field) pick(found string) string {
	if f.Type == TypeSecret {
		return found
	}
	if value := strings.TrimSpace(found); value != "" {
		return value
	}
	return strings.TrimSpace(f.Default)
}

// read returns value read by the field's type, the zero value of the type when value is empty.
func (f Field) read(value string) (Value, error) {
	parse, err := parserOf(f.Type)
	if err != nil {
		return Value{}, err
	}
	if value == "" {
		return Value{Type: f.Type}, nil
	}
	return parse(f, value)
}
