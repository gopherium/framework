// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"testing"

	"github.com/gopherium/framework/dbkit"
)

func TestEngineNamesItself(t *testing.T) {
	t.Parallel()

	cases := []struct {
		engine dbkit.Engine
		want   string
	}{
		{dbkit.Postgres, "postgres"},
		{dbkit.SQLite, "sqlite"},
		{dbkit.Engine(0), "Engine(0)"},
		{dbkit.Engine(7), "Engine(7)"},
	}
	for _, c := range cases {
		if got := c.engine.String(); got != c.want {
			t.Errorf("Engine(%d).String() = %q, want %q", int(c.engine), got, c.want)
		}
	}
}
