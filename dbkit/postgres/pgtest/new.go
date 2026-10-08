// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres"
)

// driverName is the database/sql name of the pgx driver pgtestdb connects through.
const driverName = "pgx"

var (
	// errUnparsable refuses an address pgx cannot parse, and holds none of pgx's text.
	errUnparsable = fmt.Errorf("%w: pgx cannot parse the address or one of its settings", dbkit.ErrAddress)
	// errNotURL refuses an address New cannot read as a PostgreSQL URL.
	errNotURL = fmt.Errorf("%w: pgtest needs a postgres:// or postgresql:// address it can parse", dbkit.ErrAddress)
	// errColonHost refuses an address whose host holds a colon, as an IPv6 literal does.
	errColonHost = fmt.Errorf("%w: pgtest needs a host with no colon, so an IPv6 literal must be given as a host name",
		dbkit.ErrAddress)
	// errQueryPassword refuses an address whose query sets the server password.
	errQueryPassword = fmt.Errorf("%w: the query sets password, write the server password before the @ instead",
		dbkit.ErrAddress)
	// errQuerySSLPassword refuses an address whose query sets the password of the client key.
	errQuerySSLPassword = fmt.Errorf("%w: the query sets sslpassword, set PGSSLPASSWORD instead", dbkit.ErrAddress)
	// movingKeys are the query keys that would send every database pgtestdb makes to another user, server or database.
	movingKeys = []string{"user", "host", "port", "dbname", "database"}
)

// errQueryKey returns the error for an address whose query sets the moving key.
func errQueryKey(key string) error {
	return fmt.Errorf("%w: the query sets %s, give it before the ? instead", dbkit.ErrAddress, key)
}

// New returns the escaped address of a fresh database cut from the template migrator builds on the server of address.
func New(t testing.TB, address string, migrator pgtestdb.Migrator) string {
	t.Helper()
	server, err := configOf(address)
	if err != nil {
		t.Fatal(err)
	}
	return URL(*pgtestdb.Custom(t, server, migrator))
}

// configOf returns the escaped pgtestdb configuration of the server at address, with the password pgx resolves.
func configOf(address string) (pgtestdb.Config, error) {
	if err := postgres.CheckAddress(address); err != nil {
		return pgtestdb.Config{}, err
	}
	resolved, err := pgx.ParseConfig(address)
	if err != nil {
		return pgtestdb.Config{}, errUnparsable
	}
	server, err := serverURL(address)
	if err != nil {
		return pgtestdb.Config{}, err
	}
	user, password, _ := strings.Cut(url.UserPassword(server.User.Username(), resolved.Password).String(), ":")
	return pgtestdb.Config{
		DriverName: driverName,
		Host:       server.Hostname(),
		Port:       server.Port(),
		User:       user,
		Password:   password,
		Database:   strings.TrimPrefix(server.EscapedPath(), "/"),
		Options:    server.RawQuery,
	}, nil
}

// serverURL returns the URL of address, or the error for a URL New refuses.
func serverURL(address string) (*url.URL, error) {
	server, err := url.Parse(address)
	switch {
	case err != nil || (server.Scheme != "postgres" && server.Scheme != "postgresql"):
		return nil, errNotURL
	case strings.Contains(server.Hostname(), ":"):
		return nil, errColonHost
	case server.Query().Has("password"):
		return nil, errQueryPassword
	case server.Query().Has("sslpassword"):
		return nil, errQuerySSLPassword
	}
	for _, key := range movingKeys {
		if server.Query().Has(key) {
			return nil, errQueryKey(key)
		}
	}
	return server, nil
}
