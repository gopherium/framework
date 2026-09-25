// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
)

// Migration returns the step that applies gouncer's schema.
func Migration() gonsole.Step {
	return gonsole.Step{Name: "accounts", Run: postgres.Migrate}
}
