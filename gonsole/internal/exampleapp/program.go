// SPDX-License-Identifier: Apache-2.0

// Package exampleapp builds the example program the module's tests run as its own process.
package exampleapp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gopherium/framework/gonsole"
)

// held returns the names of the reports the example program keeps.
func held() []string {
	return []string{"quarterly", "yearly"}
}

// Program returns the example program, myapp, with its report commands.
func Program() gonsole.Program {
	return gonsole.Program{
		Name:     "myapp",
		Commands: []gonsole.Command{createCommand(), listCommand(), revokeCommand()},
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
