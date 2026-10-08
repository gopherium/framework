// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"net"
	"net/url"

	"github.com/peterldowns/pgtestdb"
)

// URL returns the address of the database cfg describes, with its user, password and database name escaped.
func URL(cfg pgtestdb.Config) string {
	address := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, cfg.Port),
		Path:     "/" + cfg.Database,
		RawQuery: cfg.Options,
	}
	return address.String()
}
