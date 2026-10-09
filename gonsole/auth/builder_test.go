// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/auth"
	"github.com/gopherium/framework/gonsole/testkit"
)

// unreachable is a database address nothing answers at, so a run that reads the database setting fails.
const unreachable = "postgres://postgres@127.0.0.1:1/none?connect_timeout=1"

// The errors a failing builder, record store or release answers.
var (
	errBuild     = errors.New("the stores are out of reach")
	errInsert    = errors.New("the insert failed")
	errRelease   = errors.New("the release failed")
	errUnbounded = errors.New("the insert ran with no deadline")
)

// journal keeps, in order, what a builder, its record store and its release did.
type journal struct {
	mu     sync.Mutex
	events []string
}

// add appends event.
func (j *journal) add(event string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, event)
}

// all returns every event so far.
func (j *journal) all() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.events)
}

// inserter is a record store whose Insert runs insert, its other methods unset.
type inserter struct {
	auth.RecordStore
	insert func(ctx context.Context) error
}

// Insert runs insert.
func (i inserter) Insert(ctx context.Context, _ auth.Entry) error {
	return i.insert(ctx)
}

// journaled returns checked with a builder that journals in j and answers records and a release answering released.
func journaled(j *journal, records auth.RecordStore, released error) auth.Config {
	cfg := checked()
	cfg.Stores = func(context.Context, gonsole.Call) (auth.Stores, func(context.Context) error, error) {
		j.add("build")
		return auth.Stores{Records: records}, func(context.Context) error {
			j.add("release")
			return released
		}, nil
	}
	return cfg
}

func TestEveryCallerRunsOnTheBuiltStores(t *testing.T) {
	t.Parallel()

	address := recorded(t)
	pool := poolAt(t, address)
	var builds, releases atomic.Int32
	cfg := limited(50)
	cfg.Stores = func(context.Context, gonsole.Call) (auth.Stores, func(context.Context) error, error) {
		builds.Add(1)
		stores := auth.Stores{Accounts: postgres.NewUserStore(pool), Records: auth.PostgresRecords(pool)}
		return stores, func(context.Context) error {
			releases.Add(1)
			return nil
		}, nil
	}
	p := reading(unreachable, cfg, nil)
	apply(t, p, []string{"account:role", "editor@example.com", "author", "-yes"})

	got := testkit.Run(t, p, "", "account:records")

	want := "  admin@example.com  account:role  editor@example.com author\n"
	if got.Code != gonsole.ExitDone || !strings.HasSuffix(got.Stdout, want) {
		t.Errorf("code %d, stdout %q, stderr %q, want 0 and the role change listed", got.Code, got.Stdout, got.Stderr)
	}
	if held := account(t, storeAt(t, address), "editor@example.com"); held.Role != "author" {
		t.Errorf("role = %q, want author", held.Role)
	}
	if builds.Load() != 4 || releases.Load() != 4 {
		t.Errorf("built %d and released %d times, want 4 each: Authorize, account:role, Record, account:records",
			builds.Load(), releases.Load())
	}
}

func TestABuilderFailureReachesEveryCaller(t *testing.T) {
	t.Parallel()

	cfg := limited(50)
	cfg.Stores = func(context.Context, gonsole.Call) (auth.Stores, func(context.Context) error, error) {
		return auth.Stores{}, nil, errBuild
	}
	purge := []string{"report:purge", "-as", "admin@example.com"}
	tests := []struct {
		name string
		p    gonsole.Program
		args []string
		want string
	}{
		{"Authorize", authorizing(unreachable, cfg, nil, command("report:purge")), purge,
			"myapp: the stores are out of reach\n"},
		{"Record", recording(unreachable, cfg, nil, command("report:purge")), purge,
			"myapp: record report:purge: the stores are out of reach\n"},
		{"account:records", recording(unreachable, cfg, nil, auth.Records(cfg)), []string{"account:records"},
			"myapp: the stores are out of reach\n"},
		{"account:list", recording(unreachable, cfg, nil, auth.List(cfg)), []string{"account:list"},
			"myapp: the stores are out of reach\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := testkit.Run(t, tt.p, "", tt.args...)

			if got.Code != gonsole.ExitFailed || got.Stderr != tt.want {
				t.Errorf("code %d, stderr %q, want 1 and %q", got.Code, got.Stderr, tt.want)
			}
		})
	}
}

func TestRecordReleasesTheBuiltStoresAfterTheInsert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		inserted, release error
		code              int
		want              string
	}{
		{"when both succeed", nil, nil, gonsole.ExitDone, ""},
		{"when the insert fails", errInsert, nil, gonsole.ExitFailed, "myapp: record report:purge: the insert failed\n"},
		{"when the release fails", nil, errRelease, gonsole.ExitFailed,
			"myapp: record report:purge: the release failed\n"},
		{"when both fail", errInsert, errRelease, gonsole.ExitFailed,
			"myapp: record report:purge: the insert failed\nmyapp: the release failed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var j journal
			records := inserter{insert: func(context.Context) error {
				j.add("insert")
				return tt.inserted
			}}
			p := recording(unreachable, journaled(&j, records, tt.release), nil, command("report:purge"))

			got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

			if got.Code != tt.code || got.Stderr != tt.want {
				t.Errorf("code %d, stderr %q, want %d and %q", got.Code, got.Stderr, tt.code, tt.want)
			}
			if want := []string{"build", "insert", "release"}; !slices.Equal(j.all(), want) {
				t.Errorf("journal %q, want %q", j.all(), want)
			}
		})
	}
}

func TestRecordReleasesTheBuiltStoresWhenTheInsertPanics(t *testing.T) {
	t.Parallel()

	var j journal
	records := inserter{insert: func(context.Context) error {
		j.add("insert")
		panic("the store broke")
	}}
	p := recording(unreachable, journaled(&j, records, nil), nil, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if want := "myapp: report:purge: panic: the store broke\n"; got.Code != gonsole.ExitFailed ||
		!strings.HasPrefix(got.Stderr, want) {
		t.Errorf("code %d, stderr %q, want 1 and %q first", got.Code, got.Stderr, want)
	}
	if want := []string{"build", "insert", "release"}; !slices.Equal(j.all(), want) {
		t.Errorf("journal %q, want %q", j.all(), want)
	}
}

func TestRecordBoundsTheBuilderByItsTimeoutButNotTheRelease(t *testing.T) {
	t.Parallel()

	bounded, released := make(chan bool, 1), make(chan error, 1)
	records := inserter{insert: func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errUnbounded
		}
	}}
	cfg := checked()
	cfg.Stores = func(ctx context.Context, _ gonsole.Call) (auth.Stores, func(context.Context) error, error) {
		_, deadline := ctx.Deadline()
		bounded <- deadline
		return auth.Stores{Records: records}, func(ctx context.Context) error {
			released <- ctx.Err()
			return nil
		}, nil
	}
	settings := map[string]string{"MYAPP_COMMAND_RECORD_TIMEOUT": "50ms"}
	p := recording(unreachable, cfg, settings, command("report:purge"))

	got := testkit.Run(t, p, "", "report:purge", "-as", "admin@example.com")

	if want := "myapp: record report:purge: context deadline exceeded\n"; got.Stderr != want {
		t.Errorf("stderr %q, want %q", got.Stderr, want)
	}
	select {
	case deadline := <-bounded:
		if !deadline {
			t.Error("the builder ran with no deadline, want the record timeout")
		}
	default:
		t.Error("the builder never ran, want it to build the record's stores")
	}
	select {
	case err := <-released:
		if err != nil {
			t.Errorf("the release ran under an ended context, %v, want one the timeout leaves alive", err)
		}
	default:
		t.Error("the release never ran, want it after the insert")
	}
}
