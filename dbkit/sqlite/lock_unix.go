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
	// geteuid returns the effective user id of the process.
	geteuid = os.Geteuid
)

// lock opens the lock file, never through a link, and holds an exclusive lock on it.
func (l *fileLocker) lock(ctx context.Context) error {
	file, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("dbkit: open the migration lock %s: %w", l.path, err)
	}
	if err := l.own(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := l.take(ctx, int(file.Fd())); err != nil {
		return errors.Join(err, file.Close())
	}
	l.file = file
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
		return fmt.Errorf("dbkit: give the migration lock %s the owner of %s: %w", l.path, l.database, err)
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
			return fmt.Errorf("dbkit: take the migration lock %s: %w", l.path, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("dbkit: wait for the migration lock %s: %w", l.path, context.Cause(ctx))
		case <-deadline.C:
			return fmt.Errorf("dbkit: the migration lock %s stayed held past the lock wait of %v", l.path, l.wait)
		case <-poll.C:
		}
	}
}
