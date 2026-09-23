// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// settings returns the settings of myapp holding values.
func settings(values map[string]string) gonsole.Env {
	return gonsole.Env{Prefix: "MYAPP_", Getenv: func(key string) string { return values[key] }}
}

// errorText returns the message of err, empty when it is nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestEnvNamesASettingUnderItsPrefix(t *testing.T) {
	t.Parallel()

	env := settings(nil)

	if got := env.Key("WINDOW"); got != "MYAPP_WINDOW" {
		t.Errorf("Key() = %q, want MYAPP_WINDOW", got)
	}
	if got := env.Within("BILLING_").Key("LIMIT"); got != "MYAPP_BILLING_LIMIT" {
		t.Errorf("Within().Key() = %q, want MYAPP_BILLING_LIMIT", got)
	}
}

func TestEnvReadsAValueWithoutItsSurroundingSpaces(t *testing.T) {
	t.Parallel()

	env := settings(map[string]string{"MYAPP_OWNER": "  maria.perez@example.com \n", "MYAPP_BILLING_LIMIT": "10"})
	cases := []struct {
		name string
		env  gonsole.Env
		key  string
		want string
	}{
		{"a padded value", env, "OWNER", "maria.perez@example.com"},
		{"an unset value", env, "MISSING", ""},
		{"a value under a longer prefix", env.Within("BILLING_"), "LIMIT", "10"},
		{"a reader that is missing", gonsole.Env{Prefix: "MYAPP_"}, "OWNER", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.env.Value(tc.key); got != tc.want {
				t.Errorf("Value() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvRequiresASettingThatHoldsAValue(t *testing.T) {
	t.Parallel()

	env := settings(map[string]string{"MYAPP_DATABASE_URL": " postgres://localhost/myapp ", "MYAPP_BLANK": "   "})
	cases := []struct {
		name  string
		key   string
		value string
		err   string
	}{
		{"a set value", "DATABASE_URL", "postgres://localhost/myapp", ""},
		{"an unset value", "MISSING", "", "MYAPP_MISSING is required"},
		{"a value of spaces", "BLANK", "", "MYAPP_BLANK is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := env.Required(tc.key)

			if got != tc.value || errorText(err) != tc.err {
				t.Errorf("Required() = %q, %q, want %q, %q", got, errorText(err), tc.value, tc.err)
			}
		})
	}
}

func TestEnvReadsADuration(t *testing.T) {
	t.Parallel()

	hour := gonsole.AtMost(int64(time.Hour))
	cases := []struct {
		name   string
		value  string
		bounds []gonsole.Bound
		want   time.Duration
		err    string
	}{
		{"an unset value", "", nil, 30 * time.Second, ""},
		{"a value of spaces", "   ", nil, 30 * time.Second, ""},
		{"a plain value", "45s", nil, 45 * time.Second, ""},
		{"a padded value", " 2m ", nil, 2 * time.Minute, ""},
		{"a word", "soon", nil, 0, `MYAPP_WINDOW: must be a duration like 30s, got "soon"`},
		{"a bare number", "30", nil, 0, `MYAPP_WINDOW: must be a duration like 30s, got "30"`},
		{"zero", "0s", nil, 0, `MYAPP_WINDOW: must stand above zero, got "0s"`},
		{"a negative value", "-5s", nil, 0, `MYAPP_WINDOW: must stand above zero, got "-5s"`},
		{"zero where zero is allowed", "0s", []gonsole.Bound{gonsole.AllowZero()}, 0, ""},
		{"a negative value where zero is allowed", "-5s", []gonsole.Bound{gonsole.AllowZero()}, 0,
			`MYAPP_WINDOW: must not be negative, got "-5s"`},
		{"a value at the bound", "1h", []gonsole.Bound{hour}, time.Hour, ""},
		{"a value above the bound", "2h", []gonsole.Bound{hour}, 0,
			`MYAPP_WINDOW: must stand at or below 1h0m0s, got "2h"`},
		{"a value above the tighter of two bounds", "50m", []gonsole.Bound{gonsole.AtMost(int64(time.Minute)), hour}, 0,
			`MYAPP_WINDOW: must stand at or below 1m0s, got "50m"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_WINDOW": tc.value})

			got, err := env.Duration("WINDOW", 30*time.Second, tc.bounds...)

			if got != tc.want || errorText(err) != tc.err {
				t.Errorf("Duration() = %v, %q, want %v, %q", got, errorText(err), tc.want, tc.err)
			}
		})
	}
}

func TestEnvReadsACount(t *testing.T) {
	t.Parallel()

	hundred := gonsole.AtMost(100)
	cases := []struct {
		name   string
		value  string
		bounds []gonsole.Bound
		want   int
		err    string
	}{
		{"an unset value", "", nil, 25, ""},
		{"a plain value", "7", nil, 7, ""},
		{"a padded value", " 12 ", nil, 12, ""},
		{"a word", "many", nil, 0, `MYAPP_BATCH: must be a whole number, got "many"`},
		{"a fraction", "2.5", nil, 0, `MYAPP_BATCH: must be a whole number, got "2.5"`},
		{"a hexadecimal number", "0x10", nil, 0, `MYAPP_BATCH: must be a whole number, got "0x10"`},
		{"a number with underscores", "1_000", nil, 0, `MYAPP_BATCH: must be a whole number, got "1_000"`},
		{"zero", "0", nil, 0, `MYAPP_BATCH: must stand above zero, got "0"`},
		{"a negative value", "-3", nil, 0, `MYAPP_BATCH: must stand above zero, got "-3"`},
		{"zero where zero is allowed", "0", []gonsole.Bound{gonsole.AllowZero()}, 0, ""},
		{"a negative value where zero is allowed", "-3", []gonsole.Bound{gonsole.AllowZero()}, 0,
			`MYAPP_BATCH: must not be negative, got "-3"`},
		{"a value at the bound", "100", []gonsole.Bound{hundred}, 100, ""},
		{"a value above the bound", "101", []gonsole.Bound{hundred}, 0,
			`MYAPP_BATCH: must stand at or below 100, got "101"`},
		{"a value too large to hold", "99999999999999999999", nil, 0,
			`MYAPP_BATCH: must stand at or below ` + strconv.Itoa(math.MaxInt) + `, got "99999999999999999999"`},
		{"a value above the tighter of two bounds", "5", []gonsole.Bound{gonsole.AtMost(3), gonsole.AtMost(10)}, 0,
			`MYAPP_BATCH: must stand at or below 3, got "5"`},
		{"a value above the tighter of two bounds given last", "5", []gonsole.Bound{gonsole.AtMost(10), gonsole.AtMost(3)},
			0, `MYAPP_BATCH: must stand at or below 3, got "5"`},
		{"a value too small to hold where zero is allowed", "-99999999999999999999", []gonsole.Bound{gonsole.AllowZero()},
			0, `MYAPP_BATCH: must not be negative, got "-99999999999999999999"`},
		{"a value too large to hold under a bound", "99999999999999999999", []gonsole.Bound{hundred}, 0,
			`MYAPP_BATCH: must stand at or below 100, got "99999999999999999999"`},
		{"a value too small to hold", "-99999999999999999999", nil, 0,
			`MYAPP_BATCH: must stand above zero, got "-99999999999999999999"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_BATCH": tc.value})

			got, err := env.Count("BATCH", 25, tc.bounds...)

			if got != tc.want || errorText(err) != tc.err {
				t.Errorf("Count() = %d, %q, want %d, %q", got, errorText(err), tc.want, tc.err)
			}
		})
	}
}

func TestEnvReadsACountBeyondThirtyTwoBits(t *testing.T) {
	t.Parallel()

	if strconv.IntSize < 64 {
		t.Skip("skipping a count only a 64 bit int holds")
	}
	beyond := int64(3_000_000_000)
	env := settings(map[string]string{"MYAPP_BATCH": "3000000000"})

	got, err := env.Count("BATCH", 25)

	if int64(got) != beyond || err != nil {
		t.Errorf("Count() = %d, %v, want %d, nil", got, err, beyond)
	}
}

func TestEnvReadsAFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		value    string
		fallback bool
		want     bool
		err      string
	}{
		{"an unset value under a true fallback", "", true, true, ""},
		{"an unset value under a false fallback", "", false, false, ""},
		{"false", "false", true, false, ""},
		{"a padded capital true", " TRUE ", false, true, ""},
		{"a zero", "0", true, false, ""},
		{"a word the reader does not know", "yes", true, false, `MYAPP_VERIFY: must be true or false, got "yes"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_VERIFY": tc.value})

			got, err := env.Flag("VERIFY", tc.fallback)

			if got != tc.want || errorText(err) != tc.err {
				t.Errorf("Flag() = %t, %q, want %t, %q", got, errorText(err), tc.want, tc.err)
			}
		})
	}
}

func TestParseReadsASettingWithTheGivenParser(t *testing.T) {
	t.Parallel()

	clock := func(value string) (time.Time, error) {
		read, err := time.Parse("15:04", value)
		if err != nil {
			return time.Time{}, errors.New("must be a clock time like 03:00")
		}
		return read, nil
	}
	fallback := time.Date(0, 1, 1, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
		want  time.Time
		err   string
	}{
		{"an unset value", "", fallback, ""},
		{"a padded value", " 04:30 ", time.Date(0, 1, 1, 4, 30, 0, 0, time.UTC), ""},
		{"a value the parser refuses", "noon", time.Time{}, "MYAPP_RECONCILE_AT: must be a clock time like 03:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := settings(map[string]string{"MYAPP_RECONCILE_AT": tc.value})

			got, err := gonsole.Parse(env, "RECONCILE_AT", fallback, clock)

			if !got.Equal(tc.want) || errorText(err) != tc.err {
				t.Errorf("Parse() = %v, %q, want %v, %q", got, errorText(err), tc.want, tc.err)
			}
		})
	}
}

func TestParseKeepsTheParserErrorInItsChain(t *testing.T) {
	t.Parallel()

	refused := errors.New("must be a clock time like 03:00")
	env := settings(map[string]string{"MYAPP_RECONCILE_AT": "noon"})

	_, err := gonsole.Parse(env, "RECONCILE_AT", 0, func(string) (int, error) { return 0, refused })

	if !errors.Is(err, refused) {
		t.Errorf("errors.Is(err, refused) = false, want true")
	}
}

func TestRunHandsTheCommandTheProgramSettings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		env  gonsole.Env
		want string
	}{
		{"a program with settings", settings(map[string]string{"MYAPP_OWNER": "maria.perez@example.com"}),
			"maria.perez@example.com\n"},
		{"a program without a reader", gonsole.Env{Prefix: "MYAPP_"}, "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := echo("report:list")
			cmd.Run = func(_ context.Context, call gonsole.Call) error {
				_, err := fmt.Fprintln(call.Stdout, call.Env.Getenv(call.Env.Key("OWNER")))
				return err
			}
			p := single(cmd)
			p.Env = tc.env

			got := execute(t, p, "report:list")

			if got.stdout != tc.want {
				t.Errorf("stdout = %q, want %q, stderr %q", got.stdout, tc.want, got.stderr)
			}
		})
	}
}
