// SPDX-License-Identifier: Apache-2.0

// Package recordstest is the contract every record store of gonsole/auth keeps, run as one test suite.
package recordstest

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole/auth"
)

// The names of the rules the suite checks.
const (
	presence      = "the store is held only after its migration"
	roundTrip     = "an inserted entry lists back whole"
	noAccount     = "an actor no account answers to lists without an account"
	emptyList     = "an empty store lists an empty list"
	emptyArgs     = "an entry without arguments or flags lists them empty"
	limitAndOrder = "the newest entries list first, at most the limit of them"
	ties          = "entries applied at once list the higher id first"
)

// low, mid and high are three entry ids in rising order.
const (
	low  = "019a0000-0000-7000-8000-000000000001"
	mid  = "019a0000-0000-7000-8000-000000000002"
	high = "019a0000-0000-7000-8000-000000000003"
)

// start is the time the planted entries count from.
var start = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Fixture is one fresh record store and the hooks the suite drives it with.
type Fixture struct {
	// Records is the store under test, not yet migrated.
	Records auth.RecordStore
	// Migrate creates what the store keeps its records in.
	Migrate func(ctx context.Context) error
	// Account creates an account at the address email and returns its id.
	Account func(ctx context.Context, email string) (string, error)
	// Plant stores entry as Insert would, but as applied at appliedAt.
	Plant func(ctx context.Context, entry auth.Entry, appliedAt time.Time) error
}

// check is one rule of the contract.
type check struct {
	// name names the rule.
	name string
	// run fails t when the fixture's store breaks the rule.
	run func(t testing.TB, fixture Fixture)
}

// checks are the rules of the contract.
var checks = []check{
	{presence, heldAfterMigration},
	{roundTrip, listedWhole},
	{noAccount, unknownActor},
	{emptyList, emptyStore},
	{emptyArgs, bareEntry},
	{limitAndOrder, newestFirst},
	{ties, higherIDFirst},
}

// Run checks every rule of the contract as a parallel subtest, each on a fresh fixture that build returns.
func Run(t *testing.T, build func(t *testing.T) Fixture) {
	t.Helper()
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, build(t))
		})
	}
}

// heldAfterMigration fails t unless the store is missing before its migration and held after it.
func heldAfterMigration(t testing.TB, fixture Fixture) {
	if held(t, fixture) {
		t.Errorf("Held() = true before the migration, want false")
	}
	migrate(t, fixture)
	if !held(t, fixture) {
		t.Errorf("Held() = false after the migration, want true")
	}
}

// listedWhole fails t unless an inserted entry lists back with its account, its arguments, its flags and a time.
func listedWhole(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	id := account(t, fixture, "maria.perez@example.com")
	want := auth.Entry{
		ID: low, Actor: "maria.perez@example.com", Command: "account:role",
		Args:  []string{"editor@example.com", `two "quoted" words`},
		Flags: map[string]string{"role": "editor", "yes": "true"},
	}
	insert(t, fixture, want)
	got := only(t, fixture)
	if got.AppliedAt.IsZero() {
		t.Errorf("Latest() listed the entry with no time, want the time it was applied")
	}
	want.AccountID, want.AppliedAt = &id, got.AppliedAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Latest() listed %s, want %s", described(got), described(want))
	}
}

// unknownActor fails t unless an entry whose actor no account answers to lists without an account.
func unknownActor(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	account(t, fixture, "admin@example.com")
	stranger := entry(low)
	stranger.Actor = "nobody@example.com"
	insert(t, fixture, stranger)
	if got := only(t, fixture); got.AccountID != nil {
		t.Errorf("Latest() listed the account %s for an actor no account answers to, want none", *got.AccountID)
	}
}

// emptyStore fails t unless a store holding no entry lists an empty list, never nil.
func emptyStore(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	if listed := latest(t, fixture, 10); listed == nil || len(listed) != 0 {
		t.Errorf("Latest() = %#v on an empty store, want an empty list", listed)
	}
}

// bareEntry fails t unless an entry inserted with nil arguments and flags lists them empty, never nil.
func bareEntry(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	bare := entry(low)
	bare.Args, bare.Flags = nil, nil
	insert(t, fixture, bare)
	got := only(t, fixture)
	if got.Args == nil || len(got.Args) != 0 || got.Flags == nil || len(got.Flags) != 0 {
		t.Errorf("Latest() listed the arguments %#v and the flags %#v, want an empty list and map", got.Args, got.Flags)
	}
}

// newestFirst fails t unless the store lists its newest entries first, at most the limit of them.
func newestFirst(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	at := map[string]time.Time{high: start, low: start.Add(time.Minute), mid: start.Add(2 * time.Minute)}
	for _, id := range []string{low, mid, high} {
		plant(t, fixture, entry(id), at[id])
	}
	listed := latest(t, fixture, 2)
	if got, want := ids(listed), []string{mid, low}; !slices.Equal(got, want) {
		t.Errorf("Latest(2) listed %q, want %q", got, want)
	}
	for _, held := range listed {
		if !held.AppliedAt.Equal(at[held.ID]) {
			t.Errorf("Latest(2) listed %s as applied at %v, want %v", held.ID, held.AppliedAt, at[held.ID])
		}
	}
}

// higherIDFirst fails t unless entries applied at the same time list the higher id first.
func higherIDFirst(t testing.TB, fixture Fixture) {
	migrate(t, fixture)
	for _, id := range []string{low, high, mid} {
		plant(t, fixture, entry(id), start)
	}
	if got, want := ids(latest(t, fixture, 3)), []string{high, mid, low}; !slices.Equal(got, want) {
		t.Errorf("Latest(3) listed %q, want %q", got, want)
	}
}

// entry returns an entry under id, with no arguments and no flags.
func entry(id string) auth.Entry {
	return auth.Entry{
		ID: id, Actor: "maria.perez@example.com", Command: "account:list", Args: []string{}, Flags: map[string]string{},
	}
}

// ids returns the id of each entry.
func ids(entries []auth.Entry) []string {
	listed := make([]string, 0, len(entries))
	for _, entry := range entries {
		listed = append(listed, entry.ID)
	}
	return listed
}

// described returns entry as a failure message shows it.
func described(entry auth.Entry) string {
	account := "none"
	if entry.AccountID != nil {
		account = *entry.AccountID
	}
	return fmt.Sprintf("{ID:%s AppliedAt:%v Actor:%s AccountID:%s Command:%s Args:%q Flags:%q}", entry.ID,
		entry.AppliedAt, entry.Actor, account, entry.Command, entry.Args, entry.Flags)
}

// migrate migrates the fixture's store, failing t when it cannot.
func migrate(t testing.TB, fixture Fixture) {
	t.Helper()
	if err := fixture.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
}

// held reports whether the fixture's store is held, failing t when the store cannot tell.
func held(t testing.TB, fixture Fixture) bool {
	t.Helper()
	held, err := fixture.Records.Held(t.Context())
	if err != nil {
		t.Fatalf("Held() = %v", err)
	}
	return held
}

// account creates the account at email and returns its id, failing t when the fixture cannot.
func account(t testing.TB, fixture Fixture, email string) string {
	t.Helper()
	id, err := fixture.Account(t.Context(), email)
	if err != nil {
		t.Fatalf("Account(%s) = %v", email, err)
	}
	return id
}

// insert stores entry, failing t when the store cannot.
func insert(t testing.TB, fixture Fixture, entry auth.Entry) {
	t.Helper()
	if err := fixture.Records.Insert(t.Context(), entry); err != nil {
		t.Fatalf("Insert(%s) = %v", entry.ID, err)
	}
}

// plant stores entry as applied at appliedAt, failing t when the fixture cannot.
func plant(t testing.TB, fixture Fixture, entry auth.Entry, appliedAt time.Time) {
	t.Helper()
	if err := fixture.Plant(t.Context(), entry, appliedAt); err != nil {
		t.Fatalf("Plant(%s) = %v", entry.ID, err)
	}
}

// latest returns the store's newest entries, at most limit of them, failing t when the store cannot.
func latest(t testing.TB, fixture Fixture, limit int) []auth.Entry {
	t.Helper()
	listed, err := fixture.Records.Latest(t.Context(), limit)
	if err != nil {
		t.Fatalf("Latest(%d) = %v", limit, err)
	}
	return listed
}

// only returns the one entry the store lists, failing t when it lists another count.
func only(t testing.TB, fixture Fixture) auth.Entry {
	t.Helper()
	listed := latest(t, fixture, 10)
	if len(listed) != 1 {
		t.Fatalf("Latest(10) listed %d entries, want 1", len(listed))
	}
	return listed[0]
}
