// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"testing"
)

func TestNameJoinsThePrefixTheIDAndTheKeyInCapitals(t *testing.T) {
	cases := []struct {
		id, key, want string
	}{
		{"feed", "title", "APP_PLUGIN_FEED__TITLE"},
		{"feed", "items", "APP_PLUGIN_FEED__ITEMS"},
		{"events", "max_seats", "APP_PLUGIN_EVENTS__MAX_SEATS"},
		{"events-pro", "max_seats", "APP_PLUGIN_EVENTS_PRO__MAX_SEATS"},
		{"events-pro-max", "max_seats", "APP_PLUGIN_EVENTS_PRO_MAX__MAX_SEATS"},
		{"notes2", "page2_size", "APP_PLUGIN_NOTES2__PAGE2_SIZE"},
	}
	for _, c := range cases {
		if got := Name("APP_PLUGIN_", c.id, c.key); got != c.want {
			t.Errorf("Name(%q, %q) = %q, want %q", c.id, c.key, got, c.want)
		}
	}
}

func TestNameNeverGivesTwoSettingsOneVariable(t *testing.T) {
	ids := []string{"a", "a-b", "a-b-c", "ab", "hook", "hook-save", "x", "x-a", "x-a-b"}
	keys := []string{"b", "c", "b_c", "a_b", "budget", "save_budget", "a", "a_b_c", "b2"}
	acceptIDs(t, ids)
	acceptKeys(t, keys)
	seen := map[string][2]string{}
	for _, id := range ids {
		for _, key := range keys {
			claim(t, seen, id, key)
		}
	}
}

// acceptIDs stops the test when checkID rejects one of ids.
func acceptIDs(t *testing.T, ids []string) {
	t.Helper()
	for _, id := range ids {
		if err := checkID(id); err != nil {
			t.Fatalf("checkID(%q) = %v, the case needs an id it accepts", id, err)
		}
	}
}

// acceptKeys stops the test when checkKey rejects one of keys.
func acceptKeys(t *testing.T, keys []string) {
	t.Helper()
	for _, key := range keys {
		if err := checkKey(key, nil); err != nil {
			t.Fatalf("checkKey(%q) = %v, the case needs a key it accepts", key, err)
		}
	}
}

// claim records the variable of id and key in seen, reporting a pair that already took it.
func claim(t *testing.T, seen map[string][2]string, id, key string) {
	t.Helper()
	name := Name("APP_PLUGIN_", id, key)
	if first, taken := seen[name]; taken {
		t.Errorf("%s names both %v and [%s %s]", name, first, id, key)
	}
	seen[name] = [2]string{id, key}
}

func TestCheckKeyTakesLowercaseWordsJoinedByOneUnderscore(t *testing.T) {
	for _, key := range []string{"title", "max_seats", "a", "page2", "a_b_c", "x_1"} {
		if err := checkKey(key, []string{"job_", "db_"}); err != nil {
			t.Errorf("checkKey(%q) = %v, want nil", key, err)
		}
	}
}

func TestCheckKeyRejectsAnotherShape(t *testing.T) {
	const want = "a key is lowercase words joined by one underscore"
	keys := []string{"", "Max__Seats", "max__seats", "_title", "title_", "2title", "max-seats", "Title", "tïtle"}
	for _, key := range keys {
		err := checkKey(key, nil)
		if err == nil || err.Error() != want {
			t.Errorf("checkKey(%q) = %v, want %q", key, err, want)
		}
	}
}

func TestCheckKeyRejectsAReservedStart(t *testing.T) {
	cases := []struct {
		key      string
		reserved []string
		want     string
	}{
		{"job_every", []string{"job_", "db_"}, "a key must not start with job_ or db_"},
		{"db_conns", []string{"job_", "db_"}, "a key must not start with job_ or db_"},
		{"job_every", []string{"job_"}, "a key must not start with job_"},
		{"db_conns", []string{"job_", "tx_", "db_"}, "a key must not start with job_, tx_ or db_"},
	}
	for _, c := range cases {
		err := checkKey(c.key, c.reserved)
		if err == nil || err.Error() != c.want {
			t.Errorf("checkKey(%q, %q) = %v, want %q", c.key, c.reserved, err, c.want)
		}
	}
}

func TestCheckKeyTakesAKeyThatOnlyHoldsAReservedWord(t *testing.T) {
	for _, key := range []string{"job", "jobs", "db", "dbx_conns", "my_job_every"} {
		if err := checkKey(key, []string{"job_", "db_"}); err != nil {
			t.Errorf("checkKey(%q) = %v, want nil", key, err)
		}
	}
}

func TestCheckIDRejectsDoubledAndTrailingHyphens(t *testing.T) {
	for _, id := range []string{"notes--pro", "notes-", "a---b", "a-b-"} {
		want := "the id " + id + " must not hold two hyphens in a row or end with one when the plugin has settings"
		err := checkID(id)
		if err == nil || err.Error() != want {
			t.Errorf("checkID(%q) = %v, want %q", id, err, want)
		}
	}
}

func TestCheckIDTakesSingleHyphensBetweenWords(t *testing.T) {
	for _, id := range []string{"notes", "notes-pro", "a-b-c", "x2"} {
		if err := checkID(id); err != nil {
			t.Errorf("checkID(%q) = %v, want nil", id, err)
		}
	}
}
