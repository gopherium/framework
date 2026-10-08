// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// rootOnlyNeed starts the error of a snapshot as root into a folder another user can change.
	rootOnlyNeed = "dbkit: a snapshot as root needs a folder only root can change, "
	// maxRootLinks is the most links a walk to a snapshot folder follows, the limit of the linux kernel.
	maxRootLinks = 40
)

var (
	// flock applies or removes an advisory lock on the open file fd.
	flock = unix.Flock
	// fstat reads the facts of the open file fd.
	fstat = unix.Fstat
	// geteuid returns the effective user id of the process.
	geteuid = os.Geteuid
	// lstat reads the facts of the file at path, never through a link at its end.
	lstat = unix.Lstat
	// openFile opens the named file with the given flags and, for a file it creates, the given mode.
	openFile = os.OpenFile
)

// lock holds an exclusive lock on the lock file, waiting while another holds it.
func (l *fileLocker) lock(ctx context.Context) error {
	return l.hold(func(fd int) error { return l.take(ctx, fd) })
}

// tryLock holds an exclusive lock on the lock file, or fails with held while another holds it.
func (l *fileLocker) tryLock(held error) error {
	return l.hold(func(fd int) error { return l.try(fd, held) })
}

// hold opens the lock file, never through a link, checks it, gives a new one its owner and locks it through take.
func (l *fileLocker) hold(take func(fd int) error) error {
	file, created, err := l.open()
	if err != nil {
		return fmt.Errorf("dbkit: open the %s %s: %w", l.name, l.path, err)
	}
	if err := l.check(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if created {
		if err := l.own(file); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	if err := take(int(file.Fd())); err != nil {
		return errors.Join(err, file.Close())
	}
	l.file = file
	return nil
}

// open creates the lock file or opens the one that exists, never through a link, and reports whether it created it.
func (l *fileLocker) open() (*os.File, bool, error) {
	file, err := openFile(l.path, os.O_RDWR|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if !errors.Is(err, unix.EEXIST) {
		return file, err == nil, err
	}
	file, err = openFile(l.path, os.O_RDWR|unix.O_NOFOLLOW, 0)
	return file, false, err
}

// check returns the error for an open lock file that is not a regular file with exactly one link.
func (l *fileLocker) check(file *os.File) error {
	var st unix.Stat_t
	if err := fstat(int(file.Fd()), &st); err != nil {
		return fmt.Errorf("dbkit: check the %s %s: %w", l.name, l.path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return fmt.Errorf("dbkit: the %s %s must be a regular file with one link", l.name, l.path)
	}
	return nil
}

// own gives file the owner and group of the database file when the process runs as root.
func (l *fileLocker) own(file *os.File) error {
	if geteuid() != 0 {
		return nil
	}
	var owner unix.Stat_t
	err := unix.Stat(l.database, &owner)
	if err == nil {
		err = file.Chown(int(owner.Uid), int(owner.Gid))
	}
	if err != nil {
		return fmt.Errorf("dbkit: give the %s %s the owner of %s: %w", l.name, l.path, l.database, err)
	}
	return nil
}

// checkRootFolder returns, as root, the error for the first folder on the way from / to folder another user can change.
func checkRootFolder(folder string) error {
	if geteuid() != 0 {
		return nil
	}
	walk := &rootWalk{folder: folder, at: "/", names: strings.Split(folder, "/")}
	for len(walk.names) > 0 {
		if err := walk.step(); err != nil {
			return err
		}
	}
	return nil
}

// rootWalk is a walk from / to a snapshot folder that follows every link on the way.
type rootWalk struct {
	// folder is the snapshot folder the walk ends on.
	folder string
	// at is the folder the walk has reached, with no link in it.
	at string
	// names are the names left between at and the end of the walk.
	names []string
	// links counts the links the walk followed.
	links int
}

// step checks the first name left in the folder the walk has reached, and moves the walk past it.
func (w *rootWalk) step() error {
	next := filepath.Join(w.at, w.names[0])
	var st unix.Stat_t
	if err := lstat(next, &st); err != nil {
		return fmt.Errorf("dbkit: check the snapshot folder %s: %w", next, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		last := len(w.names) == 1
		w.at, w.names = next, w.names[1:]
		return rootOnly(next, &st, last)
	}
	w.links++
	if w.links > maxRootLinks {
		return fmt.Errorf("dbkit: check the snapshot folder %s: %w", w.folder, unix.ELOOP)
	}
	text, err := os.Readlink(next)
	if err != nil {
		return fmt.Errorf("dbkit: read the link %s: %w", next, err)
	}
	if filepath.IsAbs(text) {
		w.at = "/"
	}
	w.names = append(strings.Split(text, "/"), w.names[1:]...)
	return nil
}

// rootOnly returns the error for the folder at path with the facts st when a user other than root can change it.
func rootOnly(path string, st *unix.Stat_t, target bool) error {
	writable := st.Mode&(unix.S_IWGRP|unix.S_IWOTH) != 0
	switch {
	case st.Uid != 0:
		return fmt.Errorf(rootOnlyNeed+"and %s belongs to user %d", path, st.Uid)
	case writable && target:
		return fmt.Errorf(rootOnlyNeed+"and the target folder %s, mode %04o, is writable by its group or others",
			path, st.Mode&^unix.S_IFMT)
	case writable && st.Mode&unix.S_ISVTX == 0:
		return fmt.Errorf(rootOnlyNeed+"and %s, mode %04o, is writable by its group or others without the sticky bit",
			path, st.Mode&^unix.S_IFMT)
	}
	return nil
}

// try holds an exclusive lock on fd at once, or fails with held while another holds it.
func (l *fileLocker) try(fd int, held error) error {
	err := flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case errors.Is(err, unix.EWOULDBLOCK):
		return fmt.Errorf("dbkit: take the %s %s: %w", l.name, l.path, held)
	case err != nil:
		return fmt.Errorf("dbkit: take the %s %s: %w", l.name, l.path, err)
	}
	return nil
}

// take holds an exclusive lock on fd, trying every poll until it succeeds, the wait passes or ctx ends.
func (l *fileLocker) take(ctx context.Context, fd int) error {
	deadline := time.NewTimer(l.wait)
	defer deadline.Stop()
	poll := time.NewTicker(l.poll)
	defer poll.Stop()
	for {
		err := flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("dbkit: take the %s %s: %w", l.name, l.path, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("dbkit: wait for the %s %s: %w", l.name, l.path, context.Cause(ctx))
		case <-deadline.C:
			return fmt.Errorf("dbkit: the %s %s stayed held past the lock wait of %v", l.name, l.path, l.wait)
		case <-poll.C:
		}
	}
}
