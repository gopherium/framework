// SPDX-License-Identifier: Apache-2.0

package pgtest_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/dbkit/postgres/pgtest"
)

// sweptStale returns the name of a stale instance of the template that scope names, created and aged for the test.
func sweptStale(t *testing.T, scope string) string {
	t.Helper()
	name := "testdb_tpl_" + scope + "_inst_00000001"
	sweepCreate(t, name)
	sweepAwaitOld(t, name)
	return name
}

func TestNewSweptSweepsEachAddressOnceThenHandsOutADatabase(t *testing.T) {
	t.Parallel()

	_, names, scope := sweepInstances(t, 1)
	migrator := newMigrator()
	other := serverAddressWith(t, "application_name", serverName("swept-"))

	first := pgtest.NewSweptWithin(t, serverAddress(), sweepAnyAge, migrator, scope)
	serverDropTemplateAtEnd(t, serverDatabase(t, first))
	sweptFirst := !sweepExists(t, names[0])
	stale := sweptStale(t, scope)
	second := pgtest.NewSweptWithin(t, serverAddress(), sweepAnyAge, migrator, scope)
	keptBySameAddress := sweepExists(t, stale)
	third := pgtest.NewSweptWithin(t, other, sweepAnyAge, migrator, scope)

	if !sweptFirst || !keptBySameAddress || sweepExists(t, stale) {
		t.Errorf("the stale instances were swept by the first call %t, kept by the second %t, swept by another "+
			"address %t, want true, true and true", sweptFirst, keptBySameAddress, !sweepExists(t, stale))
	}
	for _, address := range []string{first, second, third} {
		if got := newRows(t, address); got != 1 {
			t.Errorf("a database NewSwept returned holds %d rows, want the migrated row", got)
		}
	}
}

func TestNewSweptLogsAFailedSweepAndHandsOutADatabase(t *testing.T) {
	t.Parallel()

	role, password := serverName("dbkit_swept_"), "swept-role-password"
	serverExec(t, "CREATE ROLE %I LOGIN CREATEDB PASSWORD %L", role, password)
	t.Cleanup(func() { serverExec(t, "DROP ROLE %I", role) })
	migrator := newMigrator()
	serverDropTemplateAtEnd(t, serverDatabase(t, pgtest.New(t, serverAddress(), migrator)))
	serverExec(t, "GRANT pgtdbuser TO %I", role)
	server, err := url.Parse(serverAddress())
	if err != nil {
		t.Fatal("the test server address cannot be parsed")
	}
	server.User = url.UserPassword(role, password)
	recorder := &newFatal{TB: t}
	var address string
	done := make(chan struct{})
	go func() {
		defer close(done)
		address = pgtest.NewSweptWithin(recorder, server.String(), sweepCentury, migrator, serverName("never-"))
	}()
	<-done

	logged := strings.Join(recorder.output, "\n")
	want := "dbkit: the sweep of stale test databases failed, the tests go on: dbkit: Sweep needs a role that may run " +
		"pg_stat_file"
	if recorder.message != "" || address == "" || !strings.Contains(logged, want) {
		t.Errorf("NewSwept() failed the test with %q, returned %q and logged %q, want no failure, a database and %q",
			recorder.message, address, logged, want)
	}
}

func TestNewSweptRefusesAnAgeAtOrBelowZero(t *testing.T) {
	t.Parallel()

	for age, want := range map[time.Duration]string{
		0:                "dbkit: NewSwept needs olderThan above zero, got 0s",
		-time.Nanosecond: "dbkit: NewSwept needs olderThan above zero, got -1ns",
	} {
		recorder := &newFatal{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			pgtest.NewSwept(recorder, serverAddress(), age, newMigrator())
		}()
		<-done

		if recorder.message != want {
			t.Errorf("NewSwept() with the age %v failed the test with %q, want %q", age, recorder.message, want)
		}
	}
}
