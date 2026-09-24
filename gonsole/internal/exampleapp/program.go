// SPDX-License-Identifier: Apache-2.0

// Package exampleapp builds the example program the module's tests run as its own process.
package exampleapp

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// held returns the names of the reports the example program keeps.
func held() []string {
	return []string{"quarterly", "yearly"}
}

// Program returns the example program, myapp, whose settings getenv reads.
func Program(getenv func(string) string) gonsole.Program {
	return gonsole.Program{
		Name:       "myapp",
		Env:        gonsole.Env{Prefix: "MYAPP_", Getenv: getenv},
		Database:   "DATABASE_URL",
		Serve:      serve,
		Migrations: []gonsole.Step{{Name: "reports", Run: func(context.Context, string) error { return nil }}},
		Commands:   []gonsole.Command{createCommand(), importCommand(), listCommand(), revokeCommand()},
		Plugins:    plugins,
	}
}

// plugins registers the demo plugin, reading the database setting unless the run only describes commands.
func plugins(_ context.Context, call gonsole.Call) (gonsole.Loaded, error) {
	if !call.Describe {
		if _, err := call.DatabaseURL(); err != nil {
			return gonsole.Loaded{}, err
		}
	}
	groups, err := gonsole.Walk([]demo{{}})
	return gonsole.Loaded{Groups: groups, Failed: err}, nil
}

// demo is the example program's one compiled plugin.
type demo struct{}

// ID returns the plugin's id.
func (demo) ID() string {
	return "demo"
}

// Commands returns demo:sync, which syncs the demo data.
func (demo) Commands() []gonsole.Command {
	return []gonsole.Command{{
		Name:    "demo:sync",
		Summary: "sync the demo",
		Writes:  true,
		Run: func(_ context.Context, call gonsole.Call) error {
			verb := "would sync"
			if call.Apply {
				verb = "synced"
			}
			_, err := fmt.Fprintf(call.Stdout, "%s the demo\n", verb)
			return err
		},
	}}
}

// serve answers every request with the report names until the run ends.
func serve(ctx context.Context, call gonsole.Call) error {
	timeouts, err := call.Env.Timeouts(gonsole.Timeouts{
		ReadHeader: 10 * time.Second, Read: 30 * time.Second, Idle: 120 * time.Second, Grace: 10 * time.Second,
	})
	if err != nil {
		return err
	}
	srv := gonsole.NewServer(cmp.Or(call.Env.Value("ADDR"), "localhost:8080"), http.HandlerFunc(answer), timeouts)
	return gonsole.Serve(ctx, srv, timeouts, nil, slog.New(slog.NewTextHandler(call.Stderr, nil)))
}

// answer writes the names of the reports.
func answer(w http.ResponseWriter, _ *http.Request) {
	for _, name := range held() {
		_, _ = fmt.Fprintln(w, name)
	}
}

// createCommand returns report:create, which creates one report described by the first line of its input.
func createCommand() gonsole.Command {
	return gonsole.Command{
		Name:    "report:create",
		Summary: "create a report",
		Args:    []string{"title"},
		Writes:  true,
		Run: func(_ context.Context, call gonsole.Call) error {
			description, err := bufio.NewReader(call.Stdin).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			verb := "would create"
			if call.Apply {
				verb = "created"
			}
			_, err = fmt.Fprintf(call.Stdout, "%s %s: %s\n", verb, call.Args[0], strings.TrimSpace(description))
			return err
		},
	}
}

// importCommand returns report:import, which imports one report per line of its input after saying it reads it.
func importCommand() gonsole.Command {
	return gonsole.Command{
		Name:    "report:import",
		Summary: "import one report per line of the input",
		Writes:  true,
		Run: func(_ context.Context, call gonsole.Call) error {
			if _, err := fmt.Fprintln(call.Stderr, "reading the reports to import from the input"); err != nil {
				return err
			}
			count, err := countNames(call.Stdin)
			if err != nil {
				return err
			}
			verb := "would import"
			if call.Apply {
				verb = "imported"
			}
			_, err = fmt.Fprintf(call.Stdout, "%s %d reports\n", verb, count)
			return err
		},
	}
}

// countNames returns how many lines of input hold a report name.
func countNames(input io.Reader) (int, error) {
	count := 0
	lines := bufio.NewScanner(input)
	for lines.Scan() {
		if strings.TrimSpace(lines.Text()) != "" {
			count++
		}
	}
	return count, lines.Err()
}

// listCommand returns report:list, which lists every report.
func listCommand() gonsole.Command {
	return gonsole.Command{
		Name:    "report:list",
		Summary: "list every report",
		JSON:    true,
		Run: func(_ context.Context, call gonsole.Call) error {
			if call.JSON {
				return call.Encode(map[string][]string{"reports": held()})
			}
			for _, name := range held() {
				if _, err := fmt.Fprintln(call.Stdout, name); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// revokeCommand returns report:revoke, which revokes one report.
func revokeCommand() gonsole.Command {
	return gonsole.Command{
		Name:    "report:revoke",
		Summary: "revoke one report",
		Args:    []string{"name"},
		Run: func(_ context.Context, call gonsole.Call) error {
			name := call.Args[0]
			if !slices.Contains(held(), name) {
				return fmt.Errorf("report %q does not exist", name)
			}
			_, err := fmt.Fprintf(call.Stdout, "revoked %s\n", name)
			return err
		},
	}
}
