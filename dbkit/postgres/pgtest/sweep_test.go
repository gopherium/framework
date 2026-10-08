// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

const (
	// sweepAnyAge is an age every database already has when a sweep runs.
	sweepAnyAge = time.Nanosecond
	// sweepCentury is an age no database of the test server has.
	sweepCentury = 100 * 365 * 24 * time.Hour
	// sweepYoung is an age no database these tests make reaches before its sweep.
	sweepYoung = time.Hour
	// sweepPoll is how often a test looks for the sweep in pg_stat_activity.
	sweepPoll = 10 * time.Millisecond
	// sweepClockStep is more than the wall clock of a test server is seen to step back.
	sweepClockStep = 5 * time.Second
	// sweepUnreachable is the address of a port on the loopback that nothing listens on.
	sweepUnreachable = "postgres://u:p@127.0.0.1:1/x?sslmode=disable"
)

// sweepInstances returns the addresses and names of n fresh empty instances of one new template, and its hash.
func sweepInstances(t *testing.T, n int) (addresses, names []string, scope string) {
	t.Helper()
	migrator := pgtest.Migrator(serverName("sweep-hash-"), func(context.Context, *sql.DB) error { return nil })
	for i := range n {
		address := pgtest.New(t, serverAddress(), migrator)
		name := serverDatabase(t, address)
		if i == 0 {
			serverDropTemplateAtEnd(t, name)
		}
		addresses, names = append(addresses, address), append(names, name)
	}
	sweepAwaitOld(t, names...)
	template, _, _ := strings.Cut(names[0], "_inst_")
	return addresses, names, strings.TrimPrefix(template, "testdb_tpl_")
}

// sweepAwaitOld returns once the test server reads each database of names as older than sweepClockStep.
func sweepAwaitOld(t *testing.T, names ...string) {
	t.Helper()
	observer := sweepHold(t, serverAddress())
	lookup := "SELECT count(*), count(*) FILTER (WHERE" +
		" (pg_stat_file('base/' || oid || '/PG_VERSION', true)).modification < now() - $2::interval)" +
		" FROM pg_database WHERE datname = ANY($1)"
	ticker := time.NewTicker(sweepPoll)
	defer ticker.Stop()
	for {
		var found, old int
		if err := observer.QueryRow(t.Context(), lookup, names, sweepClockStep).Scan(&found, &old); err != nil {
			t.Fatalf("read the age of the test databases: %v", err)
		}
		if found != len(names) {
			t.Fatalf("the test server holds %d of the databases %v, want all %d", found, names, len(names))
		}
		if old == len(names) {
			return
		}
		<-ticker.C
	}
}

// sweepExists reports whether the test server holds a database named name.
func sweepExists(t *testing.T, name string) bool {
	t.Helper()
	return serverHolds(t, serverAddress(), "SELECT EXISTS (SELECT FROM pg_database WHERE datname = $1)", name)
}

func TestSweepRefusesAnAgeAtOrBelowZero(t *testing.T) {
	t.Parallel()

	for age, want := range map[time.Duration]string{
		0:                "dbkit: Sweep needs olderThan above zero, got 0s",
		-time.Nanosecond: "dbkit: Sweep needs olderThan above zero, got -1ns",
	} {
		dropped, err := pgtest.Sweep(t.Context(), sweepUnreachable, age)

		if err == nil || err.Error() != want || dropped != nil {
			t.Errorf("Sweep() = %v, %v, want nothing and %q", dropped, err, want)
		}
	}
}

func TestSweepRefusesABareAt(t *testing.T) {
	t.Parallel()

	dropped, err := pgtest.Sweep(t.Context(), "postgres://u:"+newSecret+"@ss@localhost:5434/postgres", sweepCentury)

	if !errors.Is(err, dbkit.ErrAddress) || err.Error() != newBareAt || dropped != nil {
		t.Errorf("Sweep() = %v, %v, want nothing and %q", dropped, err, newBareAt)
	}
}

func TestSweepReturnsTheErrorOfTheList(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dropped, err := pgtest.Sweep(ctx, serverAddress(), sweepCentury)

	prefix := "dbkit: list the test databases: "
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), prefix) || dropped != nil {
		t.Errorf("Sweep() = %v, %v, want nothing and context.Canceled after %q", dropped, err, prefix)
	}
}

func TestSweepDropsAnOldInstanceWithNoSession(t *testing.T) {
	t.Parallel()

	_, names, scope := sweepInstances(t, 1)

	dropped, err := pgtest.SweepWithin(t.Context(), serverAddress(), sweepAnyAge, scope)

	if err != nil || !slices.Equal(dropped, names) {
		t.Errorf("Sweep() = %v, %v, want %v and nil", dropped, err, names)
	}
	if sweepExists(t, names[0]) {
		t.Errorf("%s still exists after the sweep, want it dropped", names[0])
	}
}

func TestSweepKeepsAYoungInstance(t *testing.T) {
	t.Parallel()

	_, names, scope := sweepInstances(t, 1)

	dropped, err := pgtest.SweepWithin(t.Context(), serverAddress(), sweepYoung, scope)

	if err != nil || len(dropped) != 0 {
		t.Errorf("Sweep() = %v, %v, want nothing and nil", dropped, err)
	}
	if !sweepExists(t, names[0]) {
		t.Errorf("%s is gone after the sweep, want an instance younger than %v kept", names[0], sweepYoung)
	}
}

func TestSweepKeepsEveryInstanceYoungerThanACentury(t *testing.T) {
	t.Parallel()

	_, names, _ := sweepInstances(t, 1)

	dropped, err := pgtest.Sweep(t.Context(), serverAddress(), sweepCentury)

	if err != nil || len(dropped) != 0 {
		t.Errorf("Sweep() = %v, %v, want nothing and nil", dropped, err)
	}
	if !sweepExists(t, names[0]) {
		t.Errorf("%s is gone after the sweep, want an instance younger than %v kept", names[0], sweepCentury)
	}
}

// sweepHold returns a session on the database at address, closed when the test ends.
func sweepHold(t *testing.T, address string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), address)
	if err != nil {
		t.Fatal("connect to the instance failed, want a session on it")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestSweepKeepsAnInstanceWithASession(t *testing.T) {
	t.Parallel()

	addresses, names, scope := sweepInstances(t, 1)
	sweepHold(t, addresses[0])

	dropped, err := pgtest.SweepWithin(t.Context(), serverAddress(), sweepAnyAge, scope)

	if err != nil || len(dropped) != 0 {
		t.Errorf("Sweep() = %v, %v, want nothing and nil", dropped, err)
	}
	if !sweepExists(t, names[0]) {
		t.Errorf("%s is gone after the sweep, want an instance with a session kept", names[0])
	}
}

// sweepResult is what a sweep returned.
type sweepResult struct {
	// dropped are the names the sweep returned.
	dropped []string
	// err is the error the sweep returned.
	err error
}

// sweepAwaitDrops returns once the test server shows n sessions that run a drop of the database named name.
func sweepAwaitDrops(t *testing.T, name string, n int, ran <-chan sweepResult) {
	t.Helper()
	observer := sweepHold(t, serverAddress())
	lookup := "SELECT count(*) >= $2 FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state = 'active'" +
		" AND query LIKE 'DROP DATABASE%' AND strpos(query, $1) > 0"
	ticker := time.NewTicker(sweepPoll)
	defer ticker.Stop()
	for {
		var dropping bool
		if err := observer.QueryRow(t.Context(), lookup, name, n).Scan(&dropping); err != nil {
			t.Fatalf("look for the drop in pg_stat_activity: %v", err)
		}
		if dropping {
			return
		}
		select {
		case got := <-ran:
			t.Fatalf("Sweep() = %v, %v before it was seen dropping %s", got.dropped, got.err, name)
		case <-ticker.C:
		}
	}
}

func TestSweepReturnsTheDropsBeforeAFailedOne(t *testing.T) {
	t.Parallel()

	addresses, names, scope := sweepInstances(t, 2)
	first, last := 0, 1
	if names[last] < names[first] {
		first, last = last, first
	}
	sweepHold(t, addresses[last])
	ctx, cancel := context.WithCancel(t.Context())
	ran := make(chan sweepResult, 1)

	go func() {
		dropped, err := pgtest.SweepWithin(ctx, serverAddress(), sweepAnyAge, scope)
		ran <- sweepResult{dropped: dropped, err: err}
	}()

	sweepAwaitDrops(t, names[last], 1, ran)
	cancel()
	got := <-ran
	prefix := "dbkit: drop the test database " + names[last] + ": "
	if !errors.Is(got.err, context.Canceled) || !strings.HasPrefix(got.err.Error(), prefix) {
		t.Errorf("Sweep() error = %v, want context.Canceled after %q", got.err, prefix)
	}
	if !slices.Equal(got.dropped, names[first:first+1]) {
		t.Errorf("Sweep() dropped %v, want %v, the instance before the failed drop", got.dropped, names[first:first+1])
	}
	if sweepExists(t, names[first]) || !sweepExists(t, names[last]) {
		t.Errorf("after the sweep %s exists = %v and %s exists = %v, want false and true", names[first],
			sweepExists(t, names[first]), names[last], sweepExists(t, names[last]))
	}
}

// sweepLock returns a transaction that holds a lock every drop of the database named name waits for.
func sweepLock(t *testing.T, name string) pgx.Tx {
	t.Helper()
	tx, err := sweepHold(t, serverAddress()).Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin() error = %v, want nil", err)
	}
	if _, err := tx.Exec(t.Context(), "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS 'locked'"); err != nil {
		t.Fatalf("lock the database %s: %v", name, err)
	}
	return tx
}

func TestSweepLeavesOutAnInstanceThatIsGoneBeforeItsDrop(t *testing.T) {
	t.Parallel()

	_, names, scope := sweepInstances(t, 1)
	heldLock := sweepLock(t, names[0])
	ran := make(chan sweepResult, 2)
	sweepScope := func() {
		dropped, err := pgtest.SweepWithin(t.Context(), serverAddress(), sweepAnyAge, scope)
		ran <- sweepResult{dropped: dropped, err: err}
	}

	go sweepScope()
	sweepAwaitDrops(t, names[0], 1, ran)
	go sweepScope()
	sweepAwaitDrops(t, names[0], 2, ran)
	if err := heldLock.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback() error = %v, want nil", err)
	}

	first, second := <-ran, <-ran
	if first.err != nil || second.err != nil {
		t.Errorf("Sweep() errors = %v and %v, want nil and nil", first.err, second.err)
	}
	if got := slices.Concat(first.dropped, second.dropped); !slices.Equal(got, names) {
		t.Errorf("the two sweeps returned %v and %v, want %v from one of them only", first.dropped, second.dropped, names)
	}
	if sweepExists(t, names[0]) {
		t.Errorf("%s still exists after the sweeps, want it dropped", names[0])
	}
}

// sweepCreate creates an empty database named name, dropped when the test ends.
func sweepCreate(t *testing.T, name string) {
	t.Helper()
	serverExec(t, "CREATE DATABASE %I", name)
	t.Cleanup(func() { serverExec(t, "DROP DATABASE IF EXISTS %I", name) })
}

func TestSweepKeepsEveryDatabaseThatIsNotAnInstance(t *testing.T) {
	t.Parallel()

	_, names, scope := sweepInstances(t, 1)
	template := "testdb_tpl_" + scope
	marked := template + "_inst_ffffffff"
	kept := []string{
		template,
		marked,
		template + "_inst_0000000",
		template + "_inst_000000000",
		template + "_inst_0000000g",
		template + "_inst_DEADBEEF",
		template + "0_inst_00000000",
		template + "_inst_00000000_copy",
		"copy_" + template + "_inst_00000000",
	}
	for _, name := range kept[1:] {
		sweepCreate(t, name)
	}
	serverExec(t, "ALTER DATABASE %I IS_TEMPLATE true", marked)
	t.Cleanup(func() { serverExec(t, "ALTER DATABASE %I IS_TEMPLATE false", marked) })

	dropped, err := pgtest.SweepWithin(t.Context(), serverAddress(), sweepAnyAge, scope)

	if err != nil || !slices.Equal(dropped, names) {
		t.Errorf("Sweep() = %v, %v, want only the instance %v and nil", dropped, err, names)
	}
	for _, name := range kept {
		if !sweepExists(t, name) {
			t.Errorf("%s is gone after the sweep, want every database that is not an instance kept", name)
		}
	}
}
