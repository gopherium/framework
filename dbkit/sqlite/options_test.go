// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit/sqlite"
)

const (
	// optionsBusyTimeout is a busy timeout no uncontended test waits out.
	optionsBusyTimeout = 20017 * time.Millisecond
	// optionsBusyPoll is the busy poll the tests pass, the smallest Open accepts.
	optionsBusyPoll = time.Millisecond
	// optionsCacheSize is the page cache in kibibytes the tests pass.
	optionsCacheSize = 1536
	// optionsMaxConns is the connection cap the tests pass.
	optionsMaxConns = 4
	// optionsLargestBusyTimeout is the longest busy timeout SQLite keeps.
	optionsLargestBusyTimeout = math.MaxInt32 * time.Millisecond
	// optionsPastBusyTimeout is one millisecond past the longest busy timeout SQLite keeps.
	optionsPastBusyTimeout = optionsLargestBusyTimeout + time.Millisecond
)

// testOptions returns options with every required value set and Create on.
func testOptions() sqlite.Options {
	return sqlite.Options{
		BusyTimeout: optionsBusyTimeout,
		BusyPoll:    optionsBusyPoll,
		CacheSize:   optionsCacheSize,
		MaxConns:    optionsMaxConns,
		Synchronous: sqlite.SynchronousNormal,
		Create:      true,
	}
}

// testAddress returns the address of a database file in a fresh folder, and the file's path.
func testAddress(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "site.db")
	return "sqlite:" + path, path
}

// mustNotExist fails the test when a file exists at path.
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(%s) error = %v, want the file absent", path, err)
	}
}

// limitOf returns a pointer to a journal size limit of n bytes.
func limitOf(n int64) *int64 {
	return &n
}

// badOptionCase is one Options value sqlite.Open must refuse.
type badOptionCase struct {
	name   string
	change func(*sqlite.Options)
	want   string
}

func TestOpenRefusesABadOption(t *testing.T) {
	t.Parallel()

	cases := []badOptionCase{
		{"a missing busy timeout", func(o *sqlite.Options) { o.BusyTimeout = 0 },
			"dbkit: the option BusyTimeout must be 1ms or more, got 0s"},
		{"a negative busy timeout", func(o *sqlite.Options) { o.BusyTimeout = -time.Millisecond },
			"dbkit: the option BusyTimeout must be 1ms or more, got -1ms"},
		{"a busy timeout below a millisecond", func(o *sqlite.Options) { o.BusyTimeout = time.Millisecond - 1 },
			"dbkit: the option BusyTimeout must be 1ms or more, got 999.999µs"},
		{"a busy timeout past 32-bit milliseconds", func(o *sqlite.Options) { o.BusyTimeout = optionsPastBusyTimeout },
			"dbkit: the option BusyTimeout must be 596h31m23.647s or less, got 596h31m23.648s"},
		{"a busy timeout a nanosecond past the bound",
			func(o *sqlite.Options) { o.BusyTimeout = optionsLargestBusyTimeout + time.Nanosecond },
			"dbkit: the option BusyTimeout must be 596h31m23.647s or less, got 596h31m23.647000001s"},
		{"the longest busy timeout", func(o *sqlite.Options) { o.BusyTimeout = math.MaxInt64 },
			"dbkit: the option BusyTimeout must be 596h31m23.647s or less, got 2562047h47m16.854775807s"},
		{"a missing busy poll", func(o *sqlite.Options) { o.BusyPoll = 0 },
			"dbkit: the option BusyPoll must be 1ms or more, got 0s"},
		{"a negative busy poll", func(o *sqlite.Options) { o.BusyPoll = -time.Millisecond },
			"dbkit: the option BusyPoll must be 1ms or more, got -1ms"},
		{"a busy poll below a millisecond", func(o *sqlite.Options) { o.BusyPoll = time.Millisecond - 1 },
			"dbkit: the option BusyPoll must be 1ms or more, got 999.999µs"},
		{"a busy poll past the busy timeout", func(o *sqlite.Options) { o.BusyPoll = optionsBusyTimeout + time.Millisecond },
			"dbkit: the option BusyPoll must be the BusyTimeout of 20.017s or less, got 20.018s"},
		{"a busy poll a nanosecond past the busy timeout",
			func(o *sqlite.Options) { o.BusyPoll = optionsBusyTimeout + time.Nanosecond },
			"dbkit: the option BusyPoll must be the BusyTimeout of 20.017s or less, got 20.017000001s"},
		{"a missing busy poll and cache size", func(o *sqlite.Options) { o.BusyPoll, o.CacheSize = 0, 0 },
			"dbkit: the option BusyPoll must be 1ms or more, got 0s"},
		{"a missing cache size", func(o *sqlite.Options) { o.CacheSize = 0 },
			"dbkit: the option CacheSize must stand above zero, got 0"},
		{"a negative cache size", func(o *sqlite.Options) { o.CacheSize = -1 },
			"dbkit: the option CacheSize must stand above zero, got -1"},
		{"a missing connection cap", func(o *sqlite.Options) { o.MaxConns = 0 },
			"dbkit: the option MaxConns must be 2 or more, got 0"},
		{"a negative connection cap", func(o *sqlite.Options) { o.MaxConns = -2 },
			"dbkit: the option MaxConns must be 2 or more, got -2"},
		{"a connection cap of one", func(o *sqlite.Options) { o.MaxConns = 1 },
			"dbkit: the option MaxConns must be 2 or more, got 1"},
		{"a missing synchronous mode", func(o *sqlite.Options) { o.Synchronous = 0 },
			"dbkit: the option Synchronous must be SynchronousNormal or SynchronousFull, got 0"},
		{"a negative synchronous mode", func(o *sqlite.Options) { o.Synchronous = -1 },
			"dbkit: the option Synchronous must be SynchronousNormal or SynchronousFull, got -1"},
		{"an unknown synchronous mode", func(o *sqlite.Options) { o.Synchronous = sqlite.SynchronousFull + 1 },
			"dbkit: the option Synchronous must be SynchronousNormal or SynchronousFull, got 3"},
		{"a zero journal size limit", func(o *sqlite.Options) { o.JournalSizeLimit = limitOf(0) },
			"dbkit: the option JournalSizeLimit must stand above zero when set, got 0"},
		{"a negative journal size limit", func(o *sqlite.Options) { o.JournalSizeLimit = limitOf(-1) },
			"dbkit: the option JournalSizeLimit must stand above zero when set, got -1"},
		{"a base folder with a question mark", func(o *sqlite.Options) { o.BaseFolder = "/srv/a?b" },
			`dbkit: the option BaseFolder must hold no ?, got "/srv/a?b"`},
	}

	// A cache size past 32 bits fits an int only where int is 64 bits wide;
	// on 32-bit platforms the constant would not compile, so skip the row there.
	if strconv.IntSize == 64 {
		past32 := int64(math.MaxInt32) + 1
		cases = append(cases, badOptionCase{
			name:   "a cache size past 32 bits",
			change: func(o *sqlite.Options) { o.CacheSize = int(past32) },
			want:   "dbkit: the option CacheSize must be 2147483647 or less, got 2147483648",
		})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			address, path := testAddress(t)
			opts := testOptions()
			c.change(&opts)

			db, err := sqlite.Open(address, opts)

			if db != nil {
				t.Errorf("Open() handle = %v, want nil", db)
			}
			if err == nil || err.Error() != c.want {
				t.Errorf("Open() error = %v, want %q", err, c.want)
			}
			mustNotExist(t, path)
		})
	}
}

func TestOpenAcceptsTheSmallestValues(t *testing.T) {
	t.Parallel()

	address, _ := testAddress(t)
	opts := testOptions()
	opts.BusyTimeout = time.Millisecond
	opts.BusyPoll = time.Millisecond
	opts.CacheSize = 1
	opts.MaxConns = 2
	opts.Synchronous = sqlite.SynchronousFull
	opts.JournalSizeLimit = limitOf(1)

	db, err := sqlite.Open(address, opts)
	if err != nil {
		t.Fatalf("Open() error = %v, want the smallest values accepted", err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Errorf("PingContext() error = %v, want nil", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestOpenAcceptsABusyPollEqualToTheBusyTimeout(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyPoll = opts.BusyTimeout

	mustPing(t, rulesOpen(t, opts))
}

func TestOpenKeepsTheLargestValues(t *testing.T) {
	t.Parallel()

	opts := testOptions()
	opts.BusyTimeout = optionsLargestBusyTimeout
	opts.BusyPoll = optionsLargestBusyTimeout
	opts.CacheSize = math.MaxInt32

	busy, cache := rulesOpen(t, opts), rulesOpen(t, opts)

	mustPing(t, busy)
	rulesMustAll(t, "PRAGMA cache_size", rulesRead[int64](t, cache, "PRAGMA cache_size"), -math.MaxInt32)
}
