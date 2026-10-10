// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres"
)

const (
	// openVariable names the environment variable that holds the test server address.
	openVariable = "DBKIT_TEST_POSTGRES_URL"
	// openMissing is the line the tests print when openVariable is empty.
	openMissing = "dbkit: the tests need " + openVariable +
		", the address of a PostgreSQL server they may create databases on"
	// openMaxConns is the connection cap the tests pass.
	openMaxConns = 4
	// openSmallestCap is the smallest connection cap Open accepts.
	openSmallestCap = 2
	// openShortWait is how long a test waits for a connection the cap holds back.
	openShortWait = 50 * time.Millisecond
	// openUnparsable is the error of every address pgx cannot parse.
	openUnparsable = "dbkit: refused database address: pgx cannot parse the address or one of its settings"
	// openPoolKeyValue is the value of every pool setting Open refuses.
	openPoolKeyValue = "7391"
)

// TestMain runs the tests once openVariable holds the test server address.
func TestMain(m *testing.M) {
	if strings.TrimSpace(openServerAddress()) == "" {
		_, _ = fmt.Fprintln(os.Stderr, openMissing)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// openServerAddress returns the address of the test server.
func openServerAddress() string {
	return os.Getenv(openVariable)
}

func TestTheTestsStopWhenTheServerVariableIsEmptyOrBlank(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", " \t"} {
		child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
		child.Env = append(os.Environ(), openVariable+"="+value)
		out, err := child.CombinedOutput()

		var exit *exec.ExitError
		if !errors.As(err, &exit) || !strings.Contains(string(out), openMissing) {
			t.Errorf("the tests with %s set to %q ended with %v and printed %q, want a failed run that prints %q",
				openVariable, value, err, out, openMissing)
		}
	}
}

// openServer returns the settings of the test server, with its user, password and database unescaped.
func openServer(t *testing.T) pgtestdb.Config {
	t.Helper()
	return openServerAt(t, openServerAddress())
}

// openServerAt returns the settings of the server at address, with its user, password and database unescaped.
func openServerAt(t *testing.T, address string) pgtestdb.Config {
	t.Helper()
	server, err := url.Parse(address)
	if err != nil {
		t.Fatal("the test server address cannot be parsed")
	}
	password, _ := server.User.Password()
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       server.Hostname(),
		Port:       server.Port(),
		User:       server.User.Username(),
		Password:   password,
		Database:   strings.TrimPrefix(server.Path, "/"),
		Options:    server.RawQuery,
	}
}

// openEscaped returns server with its user, password and database escaped for pgtestdb.Config.URL.
func openEscaped(server pgtestdb.Config) pgtestdb.Config {
	server.User, server.Password, _ = strings.Cut(url.UserPassword(server.User, server.Password).String(), ":")
	server.Database = url.PathEscape(server.Database)
	return server
}

// openFresh returns the escaped address of a fresh empty database on the test server, dropped when the test passes.
func openFresh(t *testing.T) string {
	t.Helper()
	return openFreshOn(t, openServer(t))
}

// openFreshOn returns the escaped address of a fresh empty database on server, dropped when the test passes.
func openFreshOn(t *testing.T, server pgtestdb.Config) string {
	t.Helper()
	instance := pgtestdb.Custom(t, openEscaped(server), pgtestdb.NoopMigrator{})
	address := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(instance.User, instance.Password),
		Host:     net.JoinHostPort(instance.Host, instance.Port),
		Path:     "/" + instance.Database,
		RawQuery: instance.Options,
	}
	return address.String()
}

// openEscapingPassword is a role password that holds characters a URL password must escape, and no quote.
const openEscapingPassword = "pass/word@with:marks?#% +"

// openServerAsRole returns the address of the test server as a new superuser role with password, dropped at the end.
func openServerAsRole(t *testing.T, password string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, openServerAddress())
	if err != nil {
		t.Fatal("connect to the test server failed, want a connection")
	}
	role := "dbkit_open_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE ROLE "+role+" LOGIN SUPERUSER PASSWORD '"+password+"'"); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create the role of the test: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP ROLE "+role); err != nil {
			t.Errorf("drop the role of the test: %v", err)
		}
		_ = admin.Close(ctx)
	})
	server, err := url.Parse(openServerAddress())
	if err != nil {
		t.Fatal("the test server address cannot be parsed")
	}
	server.User = url.UserPassword(role, password)
	return server.String()
}

// openServerOnDatabase returns the address of the test server on a new database named name, dropped at the end.
func openServerOnDatabase(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, openServerAddress())
	if err != nil {
		t.Fatal("connect to the test server failed, want a connection")
	}
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create the database of the test: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop the database of the test: %v", err)
		}
		_ = admin.Close(ctx)
	})
	server, err := url.Parse(openServerAddress())
	if err != nil {
		t.Fatal("the test server address cannot be parsed")
	}
	server.Path = "/" + name
	return server.String()
}

func TestAFreshDatabaseComesFromAServerDatabaseWhoseNameNeedsEscaping(t *testing.T) {
	t.Parallel()

	server := openServerAt(t, openServerOnDatabase(t, "dbkit_open_"+strings.ToLower(rand.Text())+"#name"))

	h := mustOpen(t, openFreshOn(t, server), openSmallestCap)

	if err := h.Pool.Ping(t.Context()); err != nil {
		t.Errorf("Ping() on the fresh database error = %v, want nil", err)
	}
}

func TestAFreshDatabaseComesFromAServerWhosePasswordNeedsEscaping(t *testing.T) {
	t.Parallel()

	server := openServerAt(t, openServerAsRole(t, openEscapingPassword))

	h := mustOpen(t, openFreshOn(t, server), openSmallestCap)

	if err := h.Pool.Ping(t.Context()); err != nil {
		t.Errorf("Ping() on the fresh database error = %v, want nil", err)
	}
}

// openUnreachable returns the address of a port on the loopback that nothing listens on.
func openUnreachable(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v, want nil", err)
	}
	host := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	return "postgres://u:p@" + host + "/x?sslmode=disable&connect_timeout=5"
}

// mustOpen opens address with a cap of maxConns and closes the handle when the test ends.
func mustOpen(t *testing.T, address string, maxConns int) *postgres.Handle {
	t.Helper()
	h, err := postgres.Open(address, postgres.Options{MaxConns: maxConns})
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
	return h
}

// openFreshHandle returns a handle capped at maxConns on a fresh empty database.
func openFreshHandle(t *testing.T, maxConns int) *postgres.Handle {
	t.Helper()
	return mustOpen(t, openFresh(t), maxConns)
}

func TestOpenRefusesABadOption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		maxConns int
		want     string
	}{
		{"a missing connection cap", 0, "dbkit: the option MaxConns must be 2 or more, got 0"},
		{"a connection cap of one", 1, "dbkit: the option MaxConns must be 2 or more, got 1"},
		{"a negative connection cap", -1, "dbkit: the option MaxConns must be 2 or more, got -1"},
		{"a connection cap past 32 bits", math.MaxInt32 + 1,
			"dbkit: the option MaxConns must be 2147483647 or less, got 2147483648"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h, err := postgres.Open(openUnreachable(t), postgres.Options{MaxConns: c.maxConns})

			if err == nil || err.Error() != c.want || h != nil {
				t.Errorf("Open() = %v, %v, want nil and %q", h, err, c.want)
			}
		})
	}
}

func TestOpenRefusesABareAt(t *testing.T) {
	t.Parallel()

	h, err := postgres.Open("postgres://u:"+addressSecret+"@ss@db/x", postgres.Options{MaxConns: openMaxConns})

	if !errors.Is(err, dbkit.ErrAddress) || err.Error() != addressBareAt || h != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", h, err, addressBareAt)
	}
}

func TestOpenRefusesAnEmptyAddress(t *testing.T) {
	t.Parallel()

	for _, address := range addressBlanks {
		h, err := postgres.Open(address, postgres.Options{MaxConns: openMaxConns})

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != addressEmpty || h != nil {
			t.Errorf("Open(a blank address of %d bytes) = %v, %v, want nil and %q", len(address), h, err, addressEmpty)
		}
	}
}

func TestOpenRefusesAnAddressPgxCannotParse(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"postgres://u:" + addressSecret + "@db:port/x",
		"host=db password=" + addressSecret + " port",
		"postgres://u:" + addressSecret + "@db/x?pool_max_conn_lifetime=many",
	} {
		h, err := postgres.Open(address, postgres.Options{MaxConns: openMaxConns})

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != openUnparsable || h != nil {
			t.Errorf("Open() = %v, %v, want nil and %q", h, err, openUnparsable)
		}
	}
}

func TestOpenNeverConnects(t *testing.T) {
	t.Parallel()

	h := mustOpen(t, openUnreachable(t), openMaxConns)

	if total, open := h.Pool.Stat().TotalConns(), h.DB.Stats().OpenConnections; total != 0 || open != 0 {
		t.Errorf("after Open the pool holds %d connections and the view %d, want none", total, open)
	}
	if got := h.Pool.Stat().MaxConns(); got != openMaxConns {
		t.Errorf("Stat().MaxConns() = %d, want %d", got, openMaxConns)
	}
	if err := h.Pool.Ping(t.Context()); err == nil {
		t.Error("Ping() error = nil, want the unreachable address to fail")
	}
}

// openPoolKeyError returns the error of an address that sets the pool setting key.
func openPoolKeyError(key string) string {
	return "dbkit: refused database address: the address sets " + key +
		", Options.MaxConns sets the cap and the pool opens no idle connection"
}

// openServiceFile returns an address that reads its settings from the service named service in a file of settings.
func openServiceFile(t *testing.T, service, settings string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pg_service.conf")
	if err := os.WriteFile(path, []byte("["+service+"]\n"+settings+"\n"), 0o600); err != nil {
		t.Fatalf("write the service file: %v", err)
	}
	return "postgres:///x?" + url.Values{"service": {service}, "servicefile": {path}}.Encode()
}

// openSettingForms returns one address per form pgx reads, each giving setting, a pool key and its value.
func openSettingForms(t *testing.T, setting string) map[string]string {
	t.Helper()
	return map[string]string{
		"a URL":                       openUnreachable(t) + "&" + setting,
		"a URL with the key escaped":  openUnreachable(t) + "&" + strings.Replace(setting, "_", "%5F", 1),
		"a keyword and value address": "host=127.0.0.1 port=1 user=u password=p dbname=x sslmode=disable " + setting,
		"a service file":              openServiceFile(t, "pool_settings", "host=127.0.0.1\nport=1\n"+setting),
	}
}

func TestOpenRefusesAPoolKeyInTheAddress(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"pool_max_conns", "pool_min_conns", "pool_min_idle_conns"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			for form, address := range openSettingForms(t, key+"="+openPoolKeyValue) {
				h, err := postgres.Open(address, postgres.Options{MaxConns: openMaxConns})

				if want := openPoolKeyError(key); !errors.Is(err, dbkit.ErrAddress) || err.Error() != want || h != nil {
					t.Errorf("Open(%s) = %v, %v, want nil and %q", form, h, err, want)
				}
				if err != nil && strings.Contains(err.Error(), openPoolKeyValue) {
					t.Errorf("Open(%s) error = %v, want no part of the value", form, err)
				}
			}
		})
	}
}

// openDuration is a pool duration an address gives, named by its sign.
type openDuration struct {
	// name says where the duration sits against zero.
	name string
	// value is the duration as the address writes it.
	value string
}

var (
	// openZero is a pool duration of zero.
	openZero = openDuration{"zero", "0s"}
	// openBelowZero is a pool duration below zero.
	openBelowZero = openDuration{"below zero", "-7391ms"}
)

func TestOpenRefusesAPoolDurationThatStopsThePool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key    string
		values []openDuration
		want   string
	}{
		{"pool_health_check_period", []openDuration{openZero, openBelowZero}, "dbkit: refused database address: " +
			"the address sets pool_health_check_period to zero or less, give it a positive duration"},
		{"pool_max_conn_lifetime", []openDuration{openZero, openBelowZero}, "dbkit: refused database address: " +
			"the address sets pool_max_conn_lifetime to zero or less, give it a positive duration"},
		{"pool_max_conn_lifetime_jitter", []openDuration{openBelowZero}, "dbkit: refused database address: " +
			"the address sets pool_max_conn_lifetime_jitter below zero, give it zero or a positive duration"},
	}
	for _, c := range cases {
		for _, value := range c.values {
			t.Run(c.key+" "+value.name, func(t *testing.T) {
				t.Parallel()
				for form, address := range openSettingForms(t, c.key+"="+value.value) {
					h, err := postgres.Open(address, postgres.Options{MaxConns: openMaxConns})

					if !errors.Is(err, dbkit.ErrAddress) || err.Error() != c.want || h != nil {
						t.Errorf("Open(%s) = %v, %v, want nil and %q", form, h, err, c.want)
					}
					if err != nil && strings.Contains(err.Error(), value.value) {
						t.Errorf("Open(%s) error = %v, want no part of the value", form, err)
					}
				}
			})
		}
	}
}

// openPingTimeout is the error of an address that sets pool_ping_timeout.
const openPingTimeout = "dbkit: refused database address: the address sets pool_ping_timeout, " +
	"which only pgx 5.11 and later read, drop it"

func TestOpenRefusesAPoolPingTimeout(t *testing.T) {
	t.Parallel()

	for form, address := range openSettingForms(t, "pool_ping_timeout="+openPoolKeyValue+"ms") {
		h, err := postgres.Open(address, postgres.Options{MaxConns: openMaxConns})

		if !errors.Is(err, dbkit.ErrAddress) || err.Error() != openPingTimeout || h != nil {
			t.Errorf("Open(%s) = %v, %v, want nil and %q", form, h, err, openPingTimeout)
		}
		if err != nil && strings.Contains(err.Error(), openPoolKeyValue) {
			t.Errorf("Open(%s) error = %v, want no part of the value", form, err)
		}
	}
}

func TestOpenTakesTheCapFromItsOptionsAndOpensNoIdleConnection(t *testing.T) {
	t.Parallel()

	h := mustOpen(t, openUnreachable(t), openMaxConns)

	config := h.Pool.Config()
	if config.MaxConns != openMaxConns || config.MinConns != 0 || config.MinIdleConns != 0 {
		t.Errorf("pool MaxConns, MinConns, MinIdleConns = %d, %d, %d, want %d, 0, 0",
			config.MaxConns, config.MinConns, config.MinIdleConns, openMaxConns)
	}
}

func TestOpenPassesTheOtherPoolSettingsToPgx(t *testing.T) {
	t.Parallel()

	address := openUnreachable(t) + "&pool_max_conn_lifetime=11m&pool_max_conn_idle_time=7m" +
		"&pool_health_check_period=13s&pool_max_conn_lifetime_jitter=3s"

	h := mustOpen(t, address, openMaxConns)

	config := h.Pool.Config()
	if config.MaxConnLifetime != 11*time.Minute || config.MaxConnIdleTime != 7*time.Minute ||
		config.HealthCheckPeriod != 13*time.Second || config.MaxConnLifetimeJitter != 3*time.Second {
		t.Errorf("pool lifetime, idle time, health check period, jitter = %v, %v, %v, %v, want 11m, 7m, 13s, 3s",
			config.MaxConnLifetime, config.MaxConnIdleTime, config.HealthCheckPeriod, config.MaxConnLifetimeJitter)
	}
}

func TestBothViewsShareTheCap(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openSmallestCap)
	ctx := t.Context()
	held, err := h.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v, want nil", err)
	}
	viewConn, err := h.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn() error = %v, want nil", err)
	}
	defer func() { _ = viewConn.Close() }()
	if got := h.Pool.Stat().AcquiredConns(); got != openSmallestCap {
		t.Fatalf("Stat().AcquiredConns() = %d, want the cap of %d", got, openSmallestCap)
	}

	for name, acquire := range map[string]func(context.Context) error{
		"the pool": func(ctx context.Context) error {
			conn, err := h.Pool.Acquire(ctx)
			if err == nil {
				conn.Release()
			}
			return err
		},
		"the view": func(ctx context.Context) error {
			conn, err := h.DB.Conn(ctx)
			if err == nil {
				_ = conn.Close()
			}
			return err
		},
	} {
		short, cancel := context.WithTimeout(ctx, openShortWait)
		err := acquire(short)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a third connection through %s: error = %v, want it held back until the deadline", name, err)
		}
	}

	released := make(chan struct{})
	third := make(chan error, 1)
	go func() {
		conn, err := h.DB.Conn(ctx)
		select {
		case <-released:
		default:
			err = errors.Join(err, errors.New("the third connection came before one was given back"))
		}
		if conn != nil {
			err = errors.Join(err, conn.Close())
		}
		third <- err
	}()
	close(released)
	held.Release()

	if err := <-third; err != nil {
		t.Errorf("the third connection through the view: %v, want it once the pool took one back", err)
	}
	if made := h.Pool.Stat().NewConnsCount(); made != openSmallestCap {
		t.Errorf("Stat().NewConnsCount() = %d, want the pool never past its cap of %d", made, openSmallestCap)
	}
}

func TestClosingTheViewLeavesThePoolOpen(t *testing.T) {
	t.Parallel()

	h := openFreshHandle(t, openMaxConns)
	ctx := t.Context()
	var one int
	if err := h.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("a query through the view: %v, want nil", err)
	}

	if err := h.DB.Close(); err != nil {
		t.Fatalf("closing the view: %v, want nil", err)
	}

	if err := h.DB.PingContext(ctx); err == nil {
		t.Error("PingContext() on the closed view error = nil, want the view closed")
	}
	var two int
	if err := h.Pool.QueryRow(ctx, "SELECT 2").Scan(&two); err != nil || two != 2 {
		t.Errorf("a query through the pool after the view closed = %d, %v, want 2 and nil", two, err)
	}
}

func TestCloseClosesTheViewAndThePool(t *testing.T) {
	t.Parallel()

	h, err := postgres.Open(openFresh(t), postgres.Options{MaxConns: openMaxConns})
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	ctx := t.Context()
	if err := h.DB.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v, want nil", err)
	}

	if err := h.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}

	if err := h.DB.PingContext(ctx); err == nil {
		t.Error("PingContext() on the view after Close error = nil, want the view closed")
	}
	if err := h.Pool.Ping(ctx); err == nil {
		t.Error("Ping() on the pool after Close error = nil, want the pool closed")
	}
}
