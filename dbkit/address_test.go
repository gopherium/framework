// SPDX-License-Identifier: Apache-2.0

package dbkit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopherium/framework/dbkit"
)

// basicSQLiteHint is the address every SQLite error tells the operator to write instead.
const basicSQLiteHint = "such as sqlite:/srv/site/site.db"

// basicAcceptedForms is the list of accepted forms the unknown scheme error names.
const basicAcceptedForms = "postgres://, postgresql:// or sqlite:"

// basicLeakedPassword is a password that must never reach an error message.
const basicLeakedPassword = "leakedSecret"

// basicRefusedAddress is an address one call refuses, with the phrase its error must hold.
type basicRefusedAddress struct {
	address string
	names   string
}

// basicSQLiteRefused lists the SQLite addresses both calls refuse, with the phrase each error holds.
var basicSQLiteRefused = []basicRefusedAddress{
	{"sqlite://x", "the path with no //"},
	{"sqlite:///srv/site/site.db", "the path with no //"},
	{"sqlite:/d/a%3Fb.db", "no ?, # or %"},
	{"sqlite:/d/a.db?mode=ro", "no ?, # or %"},
	{"sqlite:/d/a.db#x", "no ?, # or %"},
	{"sqlite:file:/d/a.db", "never a file: URI"},
	{"sqlite::memory:", "sqlite::memory: keeps no file"},
	{"sqlite:", "needs a file path"},
}

// basicUnknownSchemes lists addresses whose scheme neither engine takes.
var basicUnknownSchemes = []string{
	"mysql://user@db/site",
	"Postgres://user@db/site",
	"POSTGRESQL://user@db/site",
	"SQLite:/srv/site/site.db",
	"postgres:/user@db/site",
	"/srv/site/site.db",
	"",
}

// basicExpectRefused fails t unless err marks ErrAddress and holds names and the SQLite hint.
func basicExpectRefused(t *testing.T, address string, err error, names string) {
	t.Helper()
	if !errors.Is(err, dbkit.ErrAddress) {
		t.Fatalf("%q gave %v, want an error matching ErrAddress", address, err)
	}
	if !strings.HasPrefix(err.Error(), "dbkit: ") {
		t.Errorf("%q gave %q, want it to start with dbkit: ", address, err)
	}
	for _, phrase := range []string{names, basicSQLiteHint} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("%q gave %q, want it to hold %q", address, err, phrase)
		}
	}
}

func TestEngineFromAddress(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		address string
		want    dbkit.Engine
	}{
		{"postgres://user@db/site", dbkit.Postgres},
		{"postgresql://user@db/site", dbkit.Postgres},
		{"postgres://user@db/site?sslmode=disable#x%20", dbkit.Postgres},
		{"sqlite:/srv/site/site.db", dbkit.SQLite},
		{"sqlite:site.db", dbkit.SQLite},
	}
	for _, c := range accepted {
		got, err := dbkit.EngineOf(c.address)
		if err != nil || got != c.want {
			t.Errorf("EngineOf(%q) = %v, %v, want %v, nil", c.address, got, err, c.want)
		}
	}
	for _, c := range basicSQLiteRefused {
		got, err := dbkit.EngineOf(c.address)
		if got != 0 {
			t.Errorf("EngineOf(%q) = %v, want the zero Engine", c.address, got)
		}
		basicExpectRefused(t, c.address, err, c.names)
	}
	for _, address := range basicUnknownSchemes {
		got, err := dbkit.EngineOf(address)
		if got != 0 {
			t.Errorf("EngineOf(%q) = %v, want the zero Engine", address, got)
		}
		basicExpectRefused(t, address, err, basicAcceptedForms)
	}
}

func TestSQLitePath(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		address string
		want    string
	}{
		{"sqlite:/srv/site/site.db", "/srv/site/site.db"},
		{"sqlite:site.db", "site.db"},
		{"sqlite:./data/site.db", "./data/site.db"},
		{"sqlite:/srv/site dir/site.db", "/srv/site dir/site.db"},
	}
	for _, c := range accepted {
		got, err := dbkit.SQLitePath(c.address)
		if err != nil || got != c.want {
			t.Errorf("SQLitePath(%q) = %q, %v, want %q, nil", c.address, got, err, c.want)
		}
	}
	for _, c := range basicSQLiteRefused {
		got, err := dbkit.SQLitePath(c.address)
		if got != "" {
			t.Errorf("SQLitePath(%q) = %q, want an empty path", c.address, got)
		}
		basicExpectRefused(t, c.address, err, c.names)
	}
	for _, address := range []string{"postgres://user@db/site", "postgresql://user@db/site"} {
		got, err := dbkit.SQLitePath(address)
		if got != "" {
			t.Errorf("SQLitePath(%q) = %q, want an empty path", address, got)
		}
		basicExpectRefused(t, address, err, "a PostgreSQL address has no SQLite path")
	}
	for _, address := range basicUnknownSchemes {
		got, err := dbkit.SQLitePath(address)
		if got != "" {
			t.Errorf("SQLitePath(%q) = %q, want an empty path", address, got)
		}
		basicExpectRefused(t, address, err, basicAcceptedForms)
	}
}

func TestARefusedAddressNeverEchoesItsPassword(t *testing.T) {
	t.Parallel()

	refused := []string{
		"mysql://user:" + basicLeakedPassword + "@db/site",
		"Postgres://user:" + basicLeakedPassword + "@db/site",
		"postgres:/user:" + basicLeakedPassword + "@db/site",
		"host=db user=user password=" + basicLeakedPassword,
		"sqlite://user:" + basicLeakedPassword + "@db/site",
		"sqlite:/d/a.db?password=" + basicLeakedPassword,
	}
	for _, address := range refused {
		_, engineErr := dbkit.EngineOf(address)
		_, pathErr := dbkit.SQLitePath(address)
		for _, err := range []error{engineErr, pathErr} {
			if err == nil {
				t.Fatalf("%q was accepted, want an error", address)
			}
			if strings.Contains(err.Error(), basicLeakedPassword) || strings.Contains(err.Error(), "user") {
				t.Errorf("%q gave %q, want no part of the address in it", address, err)
			}
		}
	}
	postgres := "postgres://user:" + basicLeakedPassword + "@db/site"
	if _, err := dbkit.SQLitePath(postgres); err == nil || strings.Contains(err.Error(), basicLeakedPassword) {
		t.Errorf("SQLitePath(%q) gave %v, want an error with no password in it", postgres, err)
	}
}
