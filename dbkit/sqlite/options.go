// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopherium/framework/dbkit"
)

// maxBusyTimeout is the longest busy timeout SQLite keeps.
const maxBusyTimeout = math.MaxInt32 * time.Millisecond

// Synchronous is how often SQLite syncs the write-ahead log to disk.
type Synchronous int

const (
	// SynchronousNormal syncs the write-ahead log at each checkpoint.
	SynchronousNormal Synchronous = iota + 1
	// SynchronousFull syncs the write-ahead log at every commit.
	SynchronousFull
)

// pragma returns the value PRAGMA synchronous takes for s.
func (s Synchronous) pragma() string {
	if s == SynchronousNormal {
		return "NORMAL"
	}
	return "FULL"
}

// Options configures one SQLite handle. BusyTimeout, CacheSize, MaxConns and Synchronous are required.
type Options struct {
	// BusyTimeout is how long a connection waits for a lock, from 1ms to 596h31m23.647s.
	BusyTimeout time.Duration
	// CacheSize is the page cache of each connection in kibibytes, from 1 to 2147483647.
	CacheSize int
	// MaxConns caps the open and idle connections, 2 or more, each holding a page cache of CacheSize.
	MaxConns int
	// Synchronous is SynchronousNormal or SynchronousFull.
	Synchronous Synchronous
	// JournalSizeLimit is the size in bytes the write-ahead log keeps after a checkpoint, and nil sets no limit.
	JournalSizeLimit *int64
	// Functions are the Go functions SQL may call, one list built once per process, and nil means none.
	Functions *dbkit.FunctionList
	// BaseFolder is the absolute folder a relative path resolves against, and empty refuses a relative path.
	BaseFolder string
	// Create lets Open accept a missing file, which the first connection creates.
	Create bool
	// Logger receives the notes of the handle, and nil discards them.
	Logger *slog.Logger
}

// check returns the error for the first option Open refuses.
func (o Options) check() error {
	if err := o.checkRequired(); err != nil {
		return err
	}
	return o.checkOptional()
}

// checkRequired returns the error for the first required option that is missing or outside its range.
func (o Options) checkRequired() error {
	switch {
	case o.BusyTimeout < time.Millisecond:
		return fmt.Errorf("dbkit: the option BusyTimeout must be 1ms or more, got %v", o.BusyTimeout)
	case o.BusyTimeout > maxBusyTimeout:
		return fmt.Errorf("dbkit: the option BusyTimeout must be %v or less, got %v", maxBusyTimeout, o.BusyTimeout)
	case o.CacheSize < 1:
		return fmt.Errorf("dbkit: the option CacheSize must stand above zero, got %d", o.CacheSize)
	case o.CacheSize > math.MaxInt32:
		return fmt.Errorf("dbkit: the option CacheSize must be %d or less, got %d", math.MaxInt32, o.CacheSize)
	case o.MaxConns < 2:
		return fmt.Errorf("dbkit: the option MaxConns must be 2 or more, got %d", o.MaxConns)
	case o.Synchronous != SynchronousNormal && o.Synchronous != SynchronousFull:
		return fmt.Errorf("dbkit: the option Synchronous must be SynchronousNormal or SynchronousFull, got %d",
			o.Synchronous)
	}
	return nil
}

// checkOptional returns the error for the first optional value that is set and refused.
func (o Options) checkOptional() error {
	switch {
	case o.JournalSizeLimit != nil && *o.JournalSizeLimit < 1:
		return fmt.Errorf("dbkit: the option JournalSizeLimit must stand above zero when set, got %d",
			*o.JournalSizeLimit)
	case o.BaseFolder != "" && !filepath.IsAbs(o.BaseFolder):
		return fmt.Errorf("dbkit: the option BaseFolder must be an absolute path, got %q", o.BaseFolder)
	case strings.Contains(o.BaseFolder, "?"):
		return fmt.Errorf("dbkit: the option BaseFolder must hold no ?, got %q", o.BaseFolder)
	}
	return nil
}
