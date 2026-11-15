// SPDX-License-Identifier: Apache-2.0

// Package settings checks the settings a plugin declares, names the variable of each one and works out its value.
package settings

import (
	"encoding/json"
	"log/slog"
	"time"
)

const (
	// TypeText is any text, its surrounding spaces trimmed.
	TypeText = "text"
	// TypeCount is a whole number, within its min and max when they are given.
	TypeCount = "count"
	// TypeFlag is true or false, in any spelling strconv.ParseBool takes.
	TypeFlag = "flag"
	// TypeDuration is a duration above zero, such as 30s.
	TypeDuration = "duration"
	// TypeChoice is one of the setting's options.
	TypeChoice = "choice"
	// TypeSecret is any text, taken as written, from the environment only.
	TypeSecret = "secret"
	// TypeLines is a comma list, each item trimmed and the empty items dropped.
	TypeLines = "lines"
)

// Rules is what one application sets for every plugin: the start of each variable name and the key starts it reserves.
type Rules struct {
	Prefix   string
	Reserved []string
}

// Field is one checked setting: its key, its type, the variable that sets it and the rules its value follows.
type Field struct {
	Key     string
	Type    string
	Env     string
	Default string
	Min     *int
	Max     *int
	Options []string
}

// Schema is one plugin's checked settings, in the order its manifest lists them.
type Schema struct {
	ID     string
	Fields []Field
}

// Value is one resolved setting, with the reading of its type filled in.
type Value struct {
	Type     string
	Text     string
	Count    int
	Flag     bool
	Duration time.Duration
	Lines    []string
	Secret   Hidden
}

// Values holds one plugin's resolved settings by key.
type Values map[string]Value

// Hidden is a secret's value that prints as set or not set.
type Hidden struct {
	reveal func() string
}

// hide returns text as a Hidden, one with no value when text is empty.
func hide(text string) Hidden {
	if text == "" {
		return Hidden{}
	}
	return Hidden{reveal: func() string { return text }}
}

// Reveal returns the secret's text.
func (h Hidden) Reveal() string {
	if h.reveal == nil {
		return ""
	}
	return h.reveal()
}

// String returns set or not set.
func (h Hidden) String() string {
	if h.reveal == nil {
		return "not set"
	}
	return "set"
}

// GoString returns set or not set.
func (h Hidden) GoString() string {
	return h.String()
}

// LogValue returns set or not set.
func (h Hidden) LogValue() slog.Value {
	return slog.StringValue(h.String())
}

// MarshalJSON returns set or not set as a JSON string.
func (h Hidden) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.String())
}
