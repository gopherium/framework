// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// fsReporting returns a statfs that reports the file system magic for every path.
func fsReporting(magic uint32) func(string, *unix.Statfs_t) error {
	return func(_ string, st *unix.Statfs_t) error {
		fsSetType(&st.Type, magic)
		return nil
	}
}

// fsSetType stores magic in a Statfs_t.Type field of any width.
func fsSetType[T int32 | int64 | uint32](field *T, magic uint32) {
	*field = T(magic)
}

// fsLocal is the ext4 magic number.
const fsLocal = 0xEF53

// fsNetworkAt returns a statfs that reports NFS for path and ext4 for every other path.
func fsNetworkAt(path string) func(string, *unix.Statfs_t) error {
	return func(p string, st *unix.Statfs_t) error {
		fsSetType(&st.Type, fsLocal)
		if p == path {
			fsSetType(&st.Type, 0x6969)
		}
		return nil
	}
}

// fsRecording returns a statfs that reports ext4 for every path and records each path it reads.
func fsRecording(read *[]string) func(string, *unix.Statfs_t) error {
	return func(p string, st *unix.Statfs_t) error {
		*read = append(*read, p)
		fsSetType(&st.Type, fsLocal)
		return nil
	}
}

// fsSwap puts fake in place of statfs until the test ends.
func fsSwap(t *testing.T, fake func(string, *unix.Statfs_t) error) {
	t.Helper()
	kept := statfs
	statfs = fake
	t.Cleanup(func() { statfs = kept })
}

// fsFolder returns a fresh folder with every link in its path resolved.
func fsFolder(t *testing.T) string {
	t.Helper()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v, want nil", err)
	}
	return folder
}

// fsFile returns the path of an empty database file in a fresh folder.
func fsFile(t *testing.T) string {
	t.Helper()
	file := filepath.Join(fsFolder(t), "site.db")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	return file
}

// fsLink returns the path of a link to target in a fresh folder.
func fsLink(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(fsFolder(t), "site.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}
	return link
}

// fsNetworkError returns the error Open answers for path on NFS.
func fsNetworkError(path string) string {
	return "dbkit: " + path + " is on a network file system (NFS), where SQLite locks are unreliable, " +
		"keep the database on a local disk"
}

func TestNetworkFileSystemsAreRefused(t *testing.T) {
	t.Parallel()

	network := []struct {
		name  string
		magic uint32
	}{
		{"NFS", 0x6969},
		{"SMB", 0x517B},
		{"CIFS", 0xFF534D42},
		{"SMB2", 0xFE534D42},
		{"9P", 0x01021997},
		{"AFS", 0x5346414F},
		{"AFS", 0x6B414653},
		{"Coda", 0x73757245},
		{"NCP", 0x564C},
		{"Ceph", 0x00C36400},
		{"OCFS2", 0x7461636F},
		{"vboxsf", 0x786F4256},
		{"prl_fs", 0x7C7C6673},
		{"BeeGFS", 0x19830326},
		{"Lustre", 0x0BD00BD0},
		{"GPFS", 0x47504653},
		{"GFS2", 0x01161970},
		{"OrangeFS", 0x20030528},
		{"Panasas", 0xAAD7AAEA},
	}
	for _, c := range network {
		if got, ok := networkSystems[c.magic]; !ok || got != c.name {
			t.Errorf("networkSystems[%#x] = %q, %v, want %q refused", c.magic, got, ok, c.name)
		}
	}
	if len(networkSystems) != len(network) {
		t.Errorf("networkSystems holds %d entries, want the %d network file systems", len(networkSystems), len(network))
	}
	local := map[string]uint32{"ext4": 0xEF53, "tmpfs": 0x01021994, "FUSE": 0x65735546, "btrfs": 0x9123683E,
		"overlayfs": 0x794C7630, "XFS": 0x58465342}
	for name, magic := range local {
		if got, ok := networkSystems[magic]; ok {
			t.Errorf("networkSystems[%#x] = %q, want the local %s accepted", magic, got, name)
		}
	}
}

func TestOpenRefusesANetworkFolder(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "site.db")
	fsSwap(t, fsReporting(0x6969))

	db, err := Open("sqlite:"+path, internalOptions())

	if want := fsNetworkError(folder); err == nil || err.Error() != want || db != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%s) error = %v, want the file absent", path, err)
	}
}

func TestOpenRefusesALinkIntoANetworkFolder(t *testing.T) {
	target := fsFile(t)
	fsSwap(t, fsNetworkAt(filepath.Dir(target)))

	db, err := Open("sqlite:"+fsLink(t, target), internalOptions())

	if want := fsNetworkError(filepath.Dir(target)); err == nil || err.Error() != want || db != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
	}
}

func TestOpenRefusesAFileMountedFromANetwork(t *testing.T) {
	file := fsFile(t)
	fsSwap(t, fsNetworkAt(file))

	db, err := Open("sqlite:"+file, internalOptions())

	if want := fsNetworkError(file); err == nil || err.Error() != want || db != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
	}
}

func TestOpenRefusesALinkToAMissingFile(t *testing.T) {
	target := filepath.Join(fsFolder(t), "missing.db")
	link := fsLink(t, target)
	fsSwap(t, fsReporting(fsLocal))

	db, err := Open("sqlite:"+link, internalOptions())

	want := "dbkit: the database path " + link + " is a link to a missing file"
	if err == nil || err.Error() != want || db != nil {
		t.Errorf("Open() = %v, %v, want nil and %q", db, err, want)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(%s) error = %v, want the target absent", target, err)
	}
}

func TestOpenRefusesALinkToItself(t *testing.T) {
	folder := fsFolder(t)
	link := filepath.Join(folder, "site.db")
	if err := os.Symlink(link, link); err != nil {
		t.Fatalf("Symlink() error = %v, want nil", err)
	}
	fsSwap(t, fsReporting(fsLocal))

	db, err := Open("sqlite:"+link, internalOptions())

	if !errors.Is(err, syscall.ELOOP) || !strings.HasPrefix(err.Error(), "dbkit: check the database file: ") || db != nil {
		t.Errorf("Open() = %v, %v, want nil and the check error marked ELOOP", db, err)
	}
}

func TestOpenChecksAPlainLocalFileAndItsFolder(t *testing.T) {
	file := fsFile(t)
	var read []string
	fsSwap(t, fsRecording(&read))

	db, err := Open("sqlite:"+file, internalOptions())

	if err != nil {
		t.Fatalf("Open() error = %v, want the plain local file accepted", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if want := []string{filepath.Dir(file), file}; !slices.Equal(read, want) {
		t.Errorf("statfs read %q, want %q", read, want)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Errorf("PingContext() error = %v, want nil", err)
	}
}

func TestOpenRefusesAFolderItCannotRead(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "missing")

	db, err := Open("sqlite:"+filepath.Join(folder, "site.db"), internalOptions())

	prefix := "dbkit: read the file system of " + folder + ": "
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), prefix) || db != nil {
		t.Errorf("Open() = %v, %v, want nil and an error starting %q marked ErrNotExist", db, err, prefix)
	}
}

func TestALocalFolderIsAccepted(t *testing.T) {
	t.Parallel()

	if err := checkFolder(t.TempDir()); err != nil {
		t.Errorf("checkFolder() error = %v, want the test's local folder accepted", err)
	}
}
