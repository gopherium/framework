// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"testing"

	"github.com/gopherium/framework/dbkit"
)

func TestEscapeLikeTakesEveryPatternCharacterLiterally(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		term string
		want string
	}{
		{"an empty term", "", ""},
		{"a term with no pattern character", "plain words", "plain words"},
		{"a lone underscore", "_", `\_`},
		{"an underscore between words", "name_one", `name\_one`},
		{"a percent sign", "100%", `100\%`},
		{"a backslash", `a\b`, `a\\b`},
		{"every pattern character in a row", `\%_`, `\\\%\_`},
		{"an escape that is already written", `\_`, `\\\_`},
		{"letters beyond ASCII", "Pérez_%", `Pérez\_\%`},
	}
	for _, c := range cases {
		if got := dbkit.EscapeLike(c.term); got != c.want {
			t.Errorf("%s: EscapeLike(%q) = %q, want %q", c.name, c.term, got, c.want)
		}
	}
}
