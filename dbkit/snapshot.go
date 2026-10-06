// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"time"
)

// Snapshotter writes a consistent copy of a database to a file.
type Snapshotter interface {
	// Snapshot writes a consistent copy of the database to target and reports the checkpoint run after it.
	Snapshot(ctx context.Context, target string) (Snapshot, error)
}

// Snapshot describes one finished copy of a database.
type Snapshot struct {
	// Path is the file the copy was written to.
	Path string
	// Checkpoint is the write-ahead log checkpoint run after the copy.
	Checkpoint Checkpoint
}

// Checkpoint reports one write-ahead log checkpoint.
type Checkpoint struct {
	// Busy reports that a reader or a writer stopped the checkpoint before it finished.
	Busy bool
	// LogFrames is the count of frames in the write-ahead log, or -1 when there is none.
	LogFrames int
	// CheckpointedFrames is the count of log frames written back into the database file, or -1 when there is none.
	CheckpointedFrames int
	// Took is how long the checkpoint ran.
	Took time.Duration
}
