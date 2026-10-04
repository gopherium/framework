// SPDX-License-Identifier: Apache-2.0

package locale_test

import (
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/locale"
)

// settings returns the settings of myapp holding values.
func settings(values map[string]string) gonsole.Env {
	return gonsole.Env{Prefix: "MYAPP_", Getenv: func(key string) string { return values[key] }}
}

// errorText returns the message of err, empty when it is nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestTagReadsALanguageTagInItsCanonicalForm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"an unset value", "", "es-ES"},
		{"a value of spaces", "   ", "es-ES"},
		{"a canonical tag", "de-DE", "de-DE"},
		{"a tag in lower case", "en-gb", "en-GB"},
		{"a tag joined by an underscore", "es_ES", "es-ES"},
		{"a padded tag", " fr-FR ", "fr-FR"},
		{"a bare language", "pt", "pt"},
		{"a tag carrying an extension", "en-GB-u-nu-latn", "en-GB-u-nu-latn"},
		{"a tag carrying a private use part", "en-x-foo", "en-x-foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_FORMAT_LOCALE": tc.value})

			got, err := locale.Tag(env, "FORMAT_LOCALE", "es-ES")

			if got != tc.want || err != nil {
				t.Errorf("Tag() = %q, %v, want %q, nil", got, err, tc.want)
			}
		})
	}
}

func TestTagRefusesAValueThatNamesNoLanguage(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"not a locale", "xx-YY", "und", "es-ES-", "x-foo", "und-x-i-enochian", "und-ES"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_FORMAT_LOCALE": value})

			got, err := locale.Tag(env, "FORMAT_LOCALE", "es-ES")

			want := `MYAPP_FORMAT_LOCALE: must be a BCP 47 language tag such as es-ES or en-GB, got "` + value + `"`
			if got != "" || errorText(err) != want {
				t.Errorf("Tag() = %q, %q, want \"\", %q", got, errorText(err), want)
			}
		})
	}
}
