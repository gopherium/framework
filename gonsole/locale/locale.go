// SPDX-License-Identifier: Apache-2.0

// Package locale reads a setting that names a locale as a BCP 47 language tag.
package locale

import (
	"fmt"

	"golang.org/x/text/language"

	"github.com/gopherium/framework/gonsole"
)

// Tag returns the setting as a canonical BCP 47 language tag that names a language, the fallback when it is empty.
func Tag(e gonsole.Env, name, fallback string) (string, error) {
	return gonsole.Parse(e, name, fallback, canonical)
}

// canonical reads value as a language tag that names a language and returns its canonical form.
func canonical(value string) (string, error) {
	tag, err := language.Parse(value)
	base, _, _ := tag.Raw()
	if err != nil || base.String() == "und" {
		return "", fmt.Errorf("must be a BCP 47 language tag such as es-ES or en-GB, got %q", value)
	}
	return tag.String(), nil
}
