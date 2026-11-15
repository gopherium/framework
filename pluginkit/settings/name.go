// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// keyShape matches lowercase words of letters and digits joined by one underscore, the first starting with a letter.
var keyShape = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// Name returns the variable that sets key for plugin id under prefix.
func Name(prefix, id, key string) string {
	return prefix + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "__" + strings.ToUpper(key)
}

// checkKey rejects a key that is not lowercase words joined by one underscore or that starts with a reserved word.
func checkKey(key string, reserved []string) error {
	if !keyShape.MatchString(key) {
		return errors.New("a key is lowercase words joined by one underscore")
	}
	for _, start := range reserved {
		if strings.HasPrefix(key, start) {
			return fmt.Errorf("a key must not start with %s", orList(reserved))
		}
	}
	return nil
}

// checkID rejects the id of a plugin with settings when it holds two hyphens in a row or ends with one.
func checkID(id string) error {
	if strings.Contains(id, "--") || strings.HasSuffix(id, "-") {
		return fmt.Errorf("the id %s must not hold two hyphens in a row or end with one when the plugin has settings", id)
	}
	return nil
}

// orList joins words with commas and an or before the last.
func orList(words []string) string {
	last := len(words) - 1
	if last == 0 {
		return words[0]
	}
	return strings.Join(words[:last], ", ") + " or " + words[last]
}
