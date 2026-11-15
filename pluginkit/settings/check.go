// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/gopherium/framework/pluginkit/wire"
)

// defaultRules holds the rule each setting type sets for its default.
var defaultRules = map[string]func(field Field) error{
	TypeText:     anyDefault,
	TypeCount:    neededDefault,
	TypeFlag:     optionalDefault,
	TypeDuration: neededDefault,
	TypeChoice:   choiceDefault,
	TypeSecret:   noDefault,
	TypeLines:    anyDefault,
}

// Check checks the settings plugin id declares under rules and answers them ready to resolve.
func Check(id string, declared []wire.Setting, rules Rules) (Schema, error) {
	if len(declared) == 0 {
		return Schema{ID: id}, nil
	}
	if err := checkID(id); err != nil {
		return Schema{}, err
	}
	fields := make([]Field, 0, len(declared))
	seen := make(map[string]bool, len(declared))
	for i, setting := range declared {
		field, err := checkSetting(setting, rules.Reserved, seen)
		if err != nil {
			return Schema{}, fmt.Errorf("settings[%d] %s: %w", i, setting.Key, err)
		}
		field.Env = Name(rules.Prefix, id, field.Key)
		fields = append(fields, field)
	}
	return Schema{ID: id, Fields: fields}, nil
}

// checkSetting checks one declared setting, its key against reserved and the keys before it, and returns its field.
func checkSetting(setting wire.Setting, reserved []string, seen map[string]bool) (Field, error) {
	if err := checkKey(setting.Key, reserved); err != nil {
		return Field{}, err
	}
	if seen[setting.Key] {
		return Field{}, errors.New("the key is declared twice")
	}
	seen[setting.Key] = true
	if err := checkShape(setting); err != nil {
		return Field{}, err
	}
	field, err := fieldOf(setting)
	if err != nil {
		return Field{}, err
	}
	if err := defaultRules[field.Type](field); err != nil {
		return Field{}, err
	}
	return field, nil
}

// checkShape rejects an unknown type, an of field, and bounds or options on a type that takes none.
func checkShape(setting wire.Setting) error {
	if _, err := parserOf(setting.Type); err != nil {
		return err
	}
	if setting.Of != "" {
		return fmt.Errorf("a %s setting takes no of field", setting.Type)
	}
	bounded := setting.Min != nil || setting.Max != nil
	if bounded && setting.Type != TypeCount {
		return errors.New("min and max apply only to a count")
	}
	if len(setting.Options) > 0 && setting.Type != TypeChoice {
		return errors.New("options apply only to a choice")
	}
	return nil
}

// fieldOf returns the field of setting, its bounds read as whole numbers with min not above max.
func fieldOf(setting wire.Setting) (Field, error) {
	low, err := wholeBound("min", setting.Min)
	if err != nil {
		return Field{}, err
	}
	high, err := wholeBound("max", setting.Max)
	if err != nil {
		return Field{}, err
	}
	if upsideDown(low, high) {
		return Field{}, fmt.Errorf("min %d stands above max %d", *low, *high)
	}
	return Field{
		Key:     setting.Key,
		Type:    setting.Type,
		Default: setting.Default,
		Min:     low,
		Max:     high,
		Options: slices.Clone(setting.Options),
	}, nil
}

// wholeBound returns the bound called name as a whole number, nil when it is absent.
func wholeBound(name string, bound *float64) (*int, error) {
	if bound == nil {
		return nil, nil
	}
	n, text := *bound, strconv.FormatFloat(*bound, 'f', -1, 64)
	if n != math.Trunc(n) {
		return nil, fmt.Errorf("%s %s is not a whole number", name, text)
	}
	if n < math.MinInt || n >= -math.MinInt {
		return nil, fmt.Errorf("%s %s does not fit in a count", name, text)
	}
	whole := int(n)
	return &whole, nil
}

// upsideDown reports whether both bounds are given and low stands above high.
func upsideDown(low, high *int) bool {
	if low == nil || high == nil {
		return false
	}
	return *low > *high
}

// anyDefault takes any default.
func anyDefault(Field) error {
	return nil
}

// optionalDefault takes no default or one its type reads.
func optionalDefault(field Field) error {
	if strings.TrimSpace(field.Default) == "" {
		return nil
	}
	return readDefault(field)
}

// neededDefault rejects a missing default or one its type does not read.
func neededDefault(field Field) error {
	if strings.TrimSpace(field.Default) == "" {
		return fmt.Errorf("a %s needs a default", field.Type)
	}
	return readDefault(field)
}

// choiceDefault rejects a choice with no options, then a default that is not one of them.
func choiceDefault(field Field) error {
	if len(field.Options) == 0 {
		return errors.New("a choice needs options")
	}
	return neededDefault(field)
}

// noDefault rejects any default without quoting it.
func noDefault(field Field) error {
	if field.Default != "" {
		return errors.New("a secret takes no default, because plugin.json is public")
	}
	return nil
}

// readDefault rejects a default the field's type does not read.
func readDefault(field Field) error {
	if _, err := parsers[field.Type](field, field.Default); err != nil {
		return fmt.Errorf("the default %w", err)
	}
	return nil
}
