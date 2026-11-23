// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"slices"
	"strconv"
	"strings"
)

// FormField describes one setting for a settings form, never holding a secret's value.
type FormField struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Label   string   `json:"label,omitempty"`
	Help    string   `json:"help,omitempty"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
	Default string   `json:"default,omitempty"`
	Value   *string  `json:"value,omitempty"`
	Set     *bool    `json:"set,omitempty"`
	Source  string   `json:"source"`
	Locked  bool     `json:"locked"`
	Env     string   `json:"env"`
}

// FormFields describes each field for a settings form, in manifest order, never holding a secret's value.
func (s Schema) FormFields(values Values) []FormField {
	described := make([]FormField, 0, len(s.Fields))
	for _, field := range s.Fields {
		described = append(described, field.formField(values[field.Key]))
	}
	return described
}

// formField describes the field with value for a settings form, a secret only as set or not set.
func (f Field) formField(value Value) FormField {
	described := FormField{
		Key:     f.Key,
		Type:    f.Type,
		Label:   f.Label,
		Help:    f.Help,
		Min:     copied(f.Min),
		Max:     copied(f.Max),
		Options: slices.Clone(f.Options),
		Source:  value.Source,
		Locked:  value.Source == SourceEnvironment,
		Env:     f.Env,
	}
	if f.Type == TypeSecret {
		set := value.Secret.reveal != nil
		described.Set = &set
		return described
	}
	shown := shownAs(f.Type, value)
	described.Default, described.Value = strings.TrimSpace(f.Default), &shown
	return described
}

// shownAs returns value as the text a form shows for the setting type kind.
func shownAs(kind string, value Value) string {
	switch kind {
	case TypeCount:
		return strconv.Itoa(value.Count)
	case TypeFlag:
		return strconv.FormatBool(value.Flag)
	case TypeLines:
		return strings.Join(value.Lines, ", ")
	}
	return value.Text
}

// copied returns a new pointer to the number n points to, nil when n is nil.
func copied(n *int) *int {
	if n == nil {
		return nil
	}
	number := *n
	return &number
}
