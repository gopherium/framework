// SPDX-License-Identifier: Apache-2.0

package testkit_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/internal/exampleapp"
	"github.com/gopherium/framework/gonsole/testkit"
)

// databaseAddress is the database address the example program reads in these tests.
const databaseAddress = "postgres://localhost/myapp"

// halt is the value a stopper panics with to stop the helper it runs.
type halt struct{}

// stopper is a test that records why a helper skipped or failed it, and stops the helper.
type stopper struct {
	testing.TB
	skipped string
	failed  string
}

// Skip records why the helper skipped the test and stops it.
func (s *stopper) Skip(args ...any) {
	s.skipped = fmt.Sprint(args...)
	panic(halt{})
}

// Skipf records why the helper skipped the test and stops it.
func (s *stopper) Skipf(format string, args ...any) {
	s.skipped = fmt.Sprintf(format, args...)
	panic(halt{})
}

// SkipNow stops the helper without a reason.
func (s *stopper) SkipNow() {
	panic(halt{})
}

// Fatal records why the helper failed the test and stops it.
func (s *stopper) Fatal(args ...any) {
	s.failed = fmt.Sprint(args...)
	panic(halt{})
}

// stopped runs helper and reports whether a stopper stopped it.
func stopped(helper func()) (halted bool) {
	defer func() { _, halted = recover().(halt) }()
	helper()
	return false
}

func TestRunAnswersTheExitCodeAndTheOutput(t *testing.T) {
	t.Parallel()

	const dryRun = "myapp: dry run, nothing changed, pass -yes to apply\n"
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  testkit.Result
	}{
		{"a write that reads its input", "sales by region\n", []string{"report:create", "-yes", "Q3"},
			testkit.Result{Code: gonsole.ExitDone, Stdout: "created Q3: sales by region\n"}},
		{"a dry run", "sales by region\n", []string{"report:create", "Q3"},
			testkit.Result{Code: gonsole.ExitDone, Stdout: "would create Q3: sales by region\n", Stderr: dryRun}},
		{"a command that fails", "", []string{"report:revoke", "monthly"},
			testkit.Result{Code: gonsole.ExitFailed, Stderr: "myapp: report \"monthly\" does not exist\n"}},
		{"a command that reads a setting", "", []string{"demo:sync"},
			testkit.Result{Code: gonsole.ExitDone, Stdout: "would sync the demo\n", Stderr: dryRun}},
		{"a misused line", "", []string{"reprot"}, testkit.Result{Code: gonsole.ExitMisused,
			Stderr: "myapp: unknown command \"reprot\", run \"myapp list\" to see every command\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := exampleapp.Program(testkit.Getenv(map[string]string{"MYAPP_DATABASE_URL": databaseAddress}))

			got := testkit.Run(t, p, tc.stdin, tc.args...)

			if got != tc.want {
				t.Errorf("Run() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRunRunsUnderTheContextOfTheTest(t *testing.T) {
	t.Parallel()

	var done <-chan struct{}
	p := gonsole.Program{Name: "myapp", Commands: []gonsole.Command{{
		Name: "status", Summary: "print the status",
		Run: func(ctx context.Context, _ gonsole.Call) error {
			done = ctx.Done()
			return nil
		},
	}}}

	testkit.Run(t, p, "", "status")

	if done == nil {
		t.Error("the command ran under a context that never ends, want the test's context")
	}
}

func TestGetenvReadsOnlyTheValuesItHolds(t *testing.T) {
	t.Setenv("MYAPP_OWNER", "maria.perez@example.com")

	getenv := testkit.Getenv(map[string]string{"MYAPP_DATABASE_URL": databaseAddress})

	if got := getenv("MYAPP_DATABASE_URL"); got != databaseAddress {
		t.Errorf("getenv(MYAPP_DATABASE_URL) = %q, want %q", got, databaseAddress)
	}
	if got := getenv("MYAPP_OWNER"); got != "" {
		t.Errorf("getenv(MYAPP_OWNER) = %q, want empty, never the process environment", got)
	}
}

func TestCoverBinaryAnswersTheBinaryAndItsEnvironment(t *testing.T) {
	bindir, coverdir := t.TempDir(), t.TempDir()
	t.Setenv("MYAPP_COVER_BINDIR", bindir)
	t.Setenv("MYAPP_COVER_GOCOVERDIR", coverdir)
	t.Setenv("MYAPP_DATABASE_URL", databaseAddress)
	t.Setenv("GOCOVERDIR", filepath.Join(bindir, "elsewhere"))
	t.Setenv("KEPT", "yes")
	t.Setenv("KEPT_MYAPP_", "yes")
	s := &stopper{TB: t}
	var binary string
	var env []string

	if stopped(func() { binary, env = testkit.CoverBinary(s, "MYAPP_", "myapp") }) {
		t.Fatalf("CoverBinary skipped with %q, want the binary", s.skipped)
	}

	if want := filepath.Join(bindir, "myapp"); binary != want {
		t.Errorf("binary = %q, want %q", binary, want)
	}
	for _, kept := range []string{"KEPT=yes", "KEPT_MYAPP_=yes"} {
		if !slices.Contains(env, kept) {
			t.Errorf("env holds no %s, want every variable whose name does not start with the prefix", kept)
		}
	}
	covers := slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		return !strings.HasPrefix(entry, "MYAPP_") && !strings.HasPrefix(entry, "GOCOVERDIR=")
	})
	if want := []string{"GOCOVERDIR=" + coverdir}; !slices.Equal(covers, want) {
		t.Errorf("prefixed and cover entries = %q, want only %q", covers, want)
	}
	if env[len(env)-1] != "GOCOVERDIR="+coverdir {
		t.Errorf("last entry = %q, want the cover folder", env[len(env)-1])
	}
}

func TestCoverBinarySkipsOutsideACoverRun(t *testing.T) {
	cases := []struct {
		name     string
		bindir   string
		coverdir string
	}{
		{"no binary folder", "", "/cover"},
		{"no cover folder", "/bin", ""},
		{"neither folder", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MYAPP_COVER_BINDIR", tc.bindir)
			t.Setenv("MYAPP_COVER_GOCOVERDIR", tc.coverdir)
			s := &stopper{TB: t}

			halted := stopped(func() { testkit.CoverBinary(s, "MYAPP_", "myapp") })

			if want := "skipping binary test: run via make cover"; !halted || s.skipped != want {
				t.Errorf("CoverBinary stopped %t, skipped %q, want a skip %q", halted, s.skipped, want)
			}
		})
	}
}

func TestFreeAddrAnswersALoopbackAddressNothingListensOn(t *testing.T) {
	t.Parallel()

	address := testkit.FreeAddr(t)

	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("FreeAddr() = %q, want a 127.0.0.1 address", address)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listening on %s: %v, want a free port", address, err)
	}
	defer func() { _ = listener.Close() }()
	if listener.Addr().String() != address {
		t.Errorf("listening on %s bound %s, want the very address", address, listener.Addr())
	}
}

func TestWaitForListeningReturnsOnceTheServerListensAndDrainsTheRest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		log  string
	}{
		{"a text log", "level=INFO msg=starting\nlevel=INFO msg=listening addr=127.0.0.1:8080\n"},
		{"a JSON log", `{"level":"INFO","msg":"starting"}` + "\n" + `{"level":"INFO","msg":"listening"}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read, write := io.Pipe()
			defer func() { _ = write.Close() }()
			go func() { _, _ = io.WriteString(write, tc.log) }()

			testkit.WaitForListening(t, read)

			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(write, "level=INFO msg=\"shutting down\"\n")
				written <- err
			}()
			select {
			case err := <-written:
				if err != nil {
					t.Errorf("writing a later line: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("a later line blocked, want the rest of stderr drained")
			}
		})
	}
}

// bindFailure is the stderr line of a program whose server could not take its address.
const bindFailure = "myapp: http server: listen tcp 127.0.0.1:8080: bind: address already in use\n"

func TestWaitForListeningFailsWhenTheServerNeverListens(t *testing.T) {
	t.Parallel()

	s := &stopper{TB: t}

	halted := stopped(func() {
		testkit.WaitForListening(s, strings.NewReader(bindFailure))
	})

	if want := "server never reported listening"; !halted || s.failed != want {
		t.Errorf("WaitForListening stopped %t, failed %q, want a failure %q", halted, s.failed, want)
	}
}
