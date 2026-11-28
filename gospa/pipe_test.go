// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package gospa_test

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gopherium/framework/gospa"
)

func TestFilesAnswersNotFoundForANamedPipeWithoutWaitingOnIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatalf("Mkfifo() = %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot() = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for name, folder := range map[string]fs.FS{"os.DirFS": os.DirFS(dir), "os.OpenRoot": root.FS()} {
		answered := make(chan int, 1)
		go func() { answered <- ask(gospa.Files(folder, gospa.FilesOptions{}), http.MethodGet, "/pipe").Code }()
		select {
		case code := <-answered:
			if code != http.StatusNotFound {
				t.Errorf("GET /pipe from %s = %d, want 404", name, code)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("GET /pipe from %s still waits on the pipe after 5s, want 404", name)
		}
	}
}
