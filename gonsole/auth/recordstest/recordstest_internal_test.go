// SPDX-License-Identifier: Apache-2.0

package recordstest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gopherium/framework/gonsole/auth"
)

// errDefect is the error a store or a fixture answers when its defect asks it to fail.
var errDefect = errors.New("recordstest test: the defect failed the call")

// memory is a record store in memory, which keeps the contract unless defect names a fault.
type memory struct {
	// defect names the one fault the store or its fixture has, empty for none.
	defect string
	// migrated reports whether the fixture migrated the store.
	migrated bool
	// accounts maps each address the fixture created onto its id.
	accounts map[string]string
	// rows are the stored entries.
	rows []auth.Entry
}

// Held reports whether the store was migrated.
func (m *memory) Held(context.Context) (bool, error) {
	switch m.defect {
	case "held fails":
		return false, errDefect
	case "held before migrating":
		return true, nil
	case "never held":
		return false, nil
	}
	return m.migrated, nil
}

// Insert stores entry as applied now.
func (m *memory) Insert(_ context.Context, entry auth.Entry) error {
	if m.defect == "insert fails" {
		return errDefect
	}
	m.add(entry, time.Now())
	return nil
}

// plant stores entry as applied at at.
func (m *memory) plant(entry auth.Entry, at time.Time) error {
	if m.defect == "plant fails" {
		return errDefect
	}
	m.add(entry, at)
	return nil
}

// add stores entry as applied at at, with the account its actor names.
func (m *memory) add(entry auth.Entry, at time.Time) {
	entry.AppliedAt = at
	if id, held := m.accounts[entry.Actor]; held && m.defect != "drops the account" {
		entry.AccountID = &id
	}
	if m.defect == "invents an account" {
		invented := uuid.NewString()
		entry.AccountID = &invented
	}
	if m.defect != "keeps nil args" {
		flags := map[string]string{}
		maps.Copy(flags, entry.Flags)
		entry.Args, entry.Flags = append([]string{}, entry.Args...), flags
	}
	m.mangle(&entry)
	if m.defect != "loses the row" {
		m.rows = append(m.rows, entry)
	}
}

// mangle changes entry the way the store's defect asks.
func (m *memory) mangle(entry *auth.Entry) {
	switch m.defect {
	case "zero time":
		entry.AppliedAt = time.Time{}
	case "shifts the time":
		entry.AppliedAt = entry.AppliedAt.Add(time.Second)
	case "wrong command":
		entry.Command += ":again"
	case "mangles the args":
		entry.Args = append(entry.Args, "extra")
	}
}

// Latest returns the newest entries first, at most limit of them.
func (m *memory) Latest(_ context.Context, limit int) ([]auth.Entry, error) {
	if m.defect == "latest fails" {
		return nil, errDefect
	}
	if m.defect == "nil when empty" && len(m.rows) == 0 {
		return nil, nil
	}
	sorted := append([]auth.Entry{}, m.rows...)
	slices.SortFunc(sorted, m.newer)
	if m.defect != "ignores the limit" {
		sorted = sorted[:min(limit, len(sorted))]
	}
	return sorted, nil
}

// newer orders the newest entry first, then the higher id, or the way the store's defect asks.
func (m *memory) newer(a, b auth.Entry) int {
	switch m.defect {
	case "oldest first":
		return a.AppliedAt.Compare(b.AppliedAt)
	case "ties by id ascending":
		return cmp.Or(b.AppliedAt.Compare(a.AppliedAt), strings.Compare(a.ID, b.ID))
	}
	return cmp.Or(b.AppliedAt.Compare(a.AppliedAt), strings.Compare(b.ID, a.ID))
}

// fixtureOf returns the fixture of m.
func fixtureOf(m *memory) Fixture {
	m.accounts = map[string]string{}
	return Fixture{
		Records: m,
		Migrate: func(context.Context) error {
			if m.defect == "migrate fails" {
				return errDefect
			}
			m.migrated = true
			return nil
		},
		Account: func(_ context.Context, email string) (string, error) {
			if m.defect == "account fails" {
				return "", errDefect
			}
			id := uuid.NewString()
			m.accounts[email] = id
			return id, nil
		},
		Plant: func(_ context.Context, entry auth.Entry, at time.Time) error {
			return m.plant(entry, at)
		},
	}
}

// recorder is a testing.TB that keeps the failures a check reports and ends the check at a fatal one.
type recorder struct {
	testing.TB
	// failures are the messages the check reported.
	failures []string
}

// Helper marks nothing.
func (r *recorder) Helper() {}

// Errorf keeps the message.
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// Fatalf keeps the message and ends the check.
func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

// failures runs the check named name on the fixture of m and returns what it reported.
func failures(t *testing.T, name string, m *memory) []string {
	t.Helper()
	index := slices.IndexFunc(checks, func(c check) bool { return c.name == name })
	if index < 0 {
		t.Fatalf("no check is named %q", name)
	}
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		checks[index].run(r, fixtureOf(m))
	}()
	<-done
	return r.failures
}

func TestRunPassesASoundStore(t *testing.T) {
	t.Parallel()

	Run(t, func(*testing.T) Fixture { return fixtureOf(&memory{}) })
}

func TestEveryDefectFailsItsCheck(t *testing.T) {
	t.Parallel()

	for defect, name := range map[string]string{
		"held fails":            presence,
		"held before migrating": presence,
		"never held":            presence,
		"migrate fails":         presence,
		"insert fails":          roundTrip,
		"latest fails":          roundTrip,
		"account fails":         roundTrip,
		"loses the row":         roundTrip,
		"wrong command":         roundTrip,
		"drops the account":     roundTrip,
		"mangles the args":      roundTrip,
		"zero time":             roundTrip,
		"invents an account":    noAccount,
		"nil when empty":        emptyList,
		"keeps nil args":        emptyArgs,
		"ignores the limit":     limitAndOrder,
		"oldest first":          limitAndOrder,
		"shifts the time":       limitAndOrder,
		"plant fails":           limitAndOrder,
		"ties by id ascending":  ties,
	} {
		t.Run(defect, func(t *testing.T) {
			t.Parallel()

			if got := failures(t, name, &memory{defect: defect}); len(got) == 0 {
				t.Errorf("the check %q passed a store that %s, want a failure", name, defect)
			}
		})
	}
}
