// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var (
	// flock applies or removes an advisory lock on the open file fd.
	flock = unix.Flock
	// fstat reads the facts of the open file fd.
	fstat = unix.Fstat
	// geteuid returns the effective user id of the process.
	geteuid = os.Geteuid
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
