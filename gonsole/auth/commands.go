// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"github.com/gopherium/framework/gonsole"
)

// Commands returns every account command over cfg, in the order they are declared.
func Commands(cfg Config) []gonsole.Command {
	return []gonsole.Command{CreateAdmin(cfg), GrantRole(cfg), List(cfg), SetRole(cfg), Disable(cfg), Enable(cfg)}
}
