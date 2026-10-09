// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/gopherium/framework/gonsole"
)

// Records returns account:records, which lists the latest command records.
func Records(cfg Config) gonsole.Command {
	return gonsole.Command{
		Name:    "account:records",
		Summary: "list who applied which change, the newest first",
		Flags: func(fs *flag.FlagSet) {
			fs.Int("limit", 0, "how many `records` to list")
		},
		JSON: true,
		Run: func(ctx context.Context, call gonsole.Call) error {
			limit, err := cfg.limitOf(call)
			if err != nil {
				return err
			}
			return cfg.withStores(ctx, call, func(stores Stores) error {
				held, err := latest(ctx, stores.Records, limit)
				if err != nil {
					return err
				}
				if call.JSON {
					return call.Encode(struct {
						Records []Entry `json:"records"`
					}{held})
				}
				return lines(call.Stdout, held)
			})
		},
	}
}

// limitOf returns how many records the call asks for, -limit before the setting.
func (c Config) limitOf(call gonsole.Call) (int, error) {
	asked, set := call.Flags["limit"]
	if !set {
		return c.recordsLimit(call.Env)
	}
	limit, err := strconv.Atoi(asked)
	if err != nil || limit <= 0 {
		return 0, gonsole.Misuse(fmt.Errorf("account:records wants -limit above zero, got %s", asked))
	}
	return limit, nil
}

// recordsLimit returns how many records account:records lists, as the setting or the fallback names it.
func (c Config) recordsLimit(env gonsole.Env) (int, error) {
	limit, err := env.Count("COMMAND_RECORDS_LIMIT", c.RecordsLimit)
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, fmt.Errorf("gonsole/auth: the records limit must stand above zero, got %d", limit)
	}
	return limit, nil
}

// recordsHeld refuses a record store that lacks the table the records go to.
func recordsHeld(ctx context.Context, records RecordStore) error {
	held, err := records.Held(ctx)
	if err != nil {
		return err
	}
	if !held {
		return errors.New("the command records are missing, run migrate first")
	}
	return nil
}

// latest returns the newest entries of records, at most limit of them.
func latest(ctx context.Context, records RecordStore, limit int) ([]Entry, error) {
	if err := recordsHeld(ctx, records); err != nil {
		return nil, err
	}
	return records.Latest(ctx, limit)
}

// lines writes one aligned line per record to w: the time, the actor, the command, its arguments and its flags.
func lines(w io.Writer, held []Entry) error {
	aligned := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range held {
		line := []string{r.AppliedAt.UTC().Format(time.RFC3339), r.Actor, r.Command}
		if typed := strings.Join(append(shownAll(r.Args), flagged(r.Flags)...), " "); typed != "" {
			line = append(line, typed)
		}
		_, _ = fmt.Fprintln(aligned, strings.Join(line, "\t"))
	}
	return aligned.Flush()
}

// shownAll returns each value as show returns it.
func shownAll(values []string) []string {
	shown := make([]string, 0, len(values))
	for _, value := range values {
		shown = append(shown, show(value))
	}
	return shown
}

// flagged returns the flags as they were typed, sorted by name.
func flagged(flags map[string]string) []string {
	var typed []string
	for _, name := range slices.Sorted(maps.Keys(flags)) {
		typed = append(typed, "-"+name, show(flags[name]))
	}
	return typed
}

// show returns value as typed, quoted when empty or holding a space, a quote, a backslash or an unprintable rune.
func show(value string) string {
	if value != "" && !strings.ContainsFunc(value, unclear) {
		return value
	}
	return strconv.Quote(value)
}

// unclear reports whether r would make a value read wrongly when printed unquoted.
func unclear(r rune) bool {
	return r == '"' || r == '\\' || unicode.IsSpace(r) || !unicode.IsPrint(r)
}
