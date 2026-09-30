// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"

	"github.com/gopherium/framework/gonsole"
)

// Validate reads every setting the account hooks and account:records read.
func (c Config) Validate(env gonsole.Env) error {
	_, timeout := c.recordTimeout(env)
	_, limit := c.recordsLimit(env)
	return errors.Join(timeout, limit)
}
