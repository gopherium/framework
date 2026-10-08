// SPDX-License-Identifier: Apache-2.0

package pgtest

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// configEnvironment holds every environment variable pgx 5.10 and 5.11 read for a connection setting.
var configEnvironment = []string{
	"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGAPPNAME", "PGCONNECT_TIMEOUT",
	"PGSSLMODE", "PGSSLKEY", "PGSSLCERT", "PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
	"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ", "PGOPTIONS", "PGMINPROTOCOLVERSION",
	"PGMAXPROTOCOLVERSION", "PGCHANNELBINDING", "PGREQUIREAUTH",
}

// configPinEnvironment clears every variable of configEnvironment and points HOME and PGPASSFILE at an empty folder.
func configPinEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range configEnvironment {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PGPASSFILE", filepath.Join(home, "absent-passfile"))
}

func TestConfigOfGivesPgtestdbTheServerOfTheAddress(t *testing.T) {
	configPinEnvironment(t)

	for _, address := range []string{
		"postgres://plain_user:plain_password@db:5434/plain_database?sslmode=disable&application_name=kept_option",
		"postgresql://user%3A%40%2F%3F%23%25%20%2B:password%3A%40%2F%3F%23%25%20%2B@db:5434/" +
			"data%20base%3A%40%2F%3F%23%25%20%2B?sslmode=disable&application_name=kept%20option%26%3D",
		"postgres://db/plain_database",
	} {
		cfg, err := configOf(address)
		if err != nil {
			t.Fatalf("configOf() error = %v, want nil", err)
		}
		want, err := pgconn.ParseConfig(address)
		if err != nil {
			t.Fatal("ParseConfig() of the address failed, want it to parse")
		}

		got, err := pgconn.ParseConfig(cfg.URL())

		if err != nil {
			t.Fatal("ParseConfig() of pgtestdb's own address failed, want it to parse")
		}
		if got.Host != want.Host || got.Port != want.Port || got.Database != want.Database {
			t.Errorf("pgtestdb reaches %q, %d, %q, want %q, %d, %q",
				got.Host, got.Port, got.Database, want.Host, want.Port, want.Database)
		}
		if got.User != want.User || got.Password != want.Password {
			t.Errorf("pgtestdb connects as %q with a password of %d bytes, want %q with %d bytes",
				got.User, len(got.Password), want.User, len(want.Password))
		}
		if (got.TLSConfig == nil) != (want.TLSConfig == nil) || !maps.Equal(got.RuntimeParams, want.RuntimeParams) {
			t.Errorf("pgtestdb reads TLS off = %v and runtime parameters %v, want %v and %v",
				got.TLSConfig == nil, got.RuntimeParams, want.TLSConfig == nil, want.RuntimeParams)
		}
		if cfg.DriverName != driverName {
			t.Errorf("DriverName = %q, want %q", cfg.DriverName, driverName)
		}
	}
}

func TestConfigOfTakesThePasswordPgxResolvesForAnAddressWithNone(t *testing.T) {
	cases := []struct {
		name    string
		resolve func(t *testing.T, password string)
	}{
		{"PGPASSWORD", func(t *testing.T, password string) {
			t.Setenv("PGPASSWORD", password)
		}},
		{"a passfile", func(t *testing.T, password string) {
			passfile := filepath.Join(t.TempDir(), "passfile")
			if err := os.WriteFile(passfile, []byte("db:5434:plain_database:plain_user:"+password+"\n"), 0o600); err != nil {
				t.Fatalf("write the passfile: %v", err)
			}
			t.Setenv("PGPASSFILE", passfile)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configPinEnvironment(t)
			resolved := "password from " + c.name + "@/?#% +"
			c.resolve(t, resolved)

			cfg, err := configOf("postgres://plain_user@db:5434/plain_database?sslmode=disable")

			if err != nil {
				t.Fatalf("configOf() error = %v, want nil", err)
			}
			got, err := pgconn.ParseConfig(cfg.URL())
			if err != nil {
				t.Fatal("ParseConfig() of pgtestdb's own address failed, want it to parse")
			}
			if got.User != "plain_user" || got.Password != resolved {
				t.Errorf("pgtestdb connects as %q with a password of %d bytes, want %q with the %d bytes of %s",
					got.User, len(got.Password), "plain_user", len(resolved), c.name)
			}
		})
	}
}
