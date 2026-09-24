// SPDX-License-Identifier: Apache-2.0

// Package testkit runs programs built on gonsole from tests, in process and as built binaries.
package testkit

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// Result is what one in process run answers.
type Result struct {
	// Code is the exit code the run returned.
	Code int
	// Stdout is everything the run wrote to standard output.
	Stdout string
	// Stderr is everything the run wrote to standard error.
	Stderr string
}

// Run runs p with args in process, feeding stdin, and answers its exit code and output.
func Run(t testing.TB, p gonsole.Program, stdin string, args ...string) Result {
	t.Helper()
	var stdout, stderr strings.Builder
	code := p.Run(t.Context(), args, strings.NewReader(stdin), &stdout, &stderr)
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// Getenv returns a getenv that reads only values.
func Getenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

// CoverBinary returns the cover built binary called name and its environment, skipping outside a cover run.
func CoverBinary(t testing.TB, prefix, name string) (string, []string) {
	t.Helper()
	bindir, coverdir := os.Getenv(prefix+"COVER_BINDIR"), os.Getenv(prefix+"COVER_GOCOVERDIR")
	if bindir == "" || coverdir == "" {
		t.Skip("skipping binary test: run via make cover")
	}
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, prefix) || strings.HasPrefix(entry, "GOCOVERDIR=")
	})
	return filepath.Join(bindir, name), append(env, "GOCOVERDIR="+coverdir)
}

// FreeAddr returns a loopback address whose port nothing listens on.
func FreeAddr(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testkit: reserve a port: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// WaitForListening blocks until the server logs on stderr that it is listening.
func WaitForListening(t testing.TB, stderr io.Reader) {
	t.Helper()
	lines := bufio.NewScanner(stderr)
	for lines.Scan() {
		if strings.Contains(lines.Text(), "listening") {
			go func() { _, _ = io.Copy(io.Discard, stderr) }()
			return
		}
	}
	t.Fatal("server never reported listening")
}
