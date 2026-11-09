// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// usedBytes reads the bytes the used pages of the main database hold.
const usedBytes = "SELECT ((SELECT page_count FROM pragma_page_count) - " +
	"(SELECT freelist_count FROM pragma_freelist_count)) * (SELECT page_size FROM pragma_page_size)"

// ErrNoSpace is the error of a snapshot whose check before the copy finds too little free space for it and the margin.
var ErrNoSpace = errors.New("dbkit: the snapshot folder has too little free space for the copy and the margin")

// SpaceError reports a snapshot folder with less free space than the copy and the margin need.
type SpaceError struct {
	// Folder is the target folder of the snapshot.
	Folder string
	// Needed is the bytes the used pages of the database and the margin hold.
	Needed int64
	// Free is the bytes the file system of Folder leaves free to a user who is not root.
	Free int64
}

// Error returns the folder, its free bytes and the bytes needed.
func (e *SpaceError) Error() string {
	return fmt.Sprintf("dbkit: the snapshot folder %s has %d bytes free, and the copy and the margin need %d",
		e.Folder, e.Free, e.Needed)
}

// Is reports whether target is ErrNoSpace.
func (e *SpaceError) Is(target error) bool {
	return target == ErrNoSpace
}

// checkSpace returns a SpaceError when folder has less free space than the used pages of the database and the margin.
func (s *snapshotter) checkSpace(ctx context.Context, folder string) error {
	var used int64
	if err := s.db.QueryRowContext(ctx, usedBytes).Scan(&used); err != nil {
		return fmt.Errorf("dbkit: read the size of the database: %w", err)
	}
	free, err := freeSpace(folder)
	if err != nil {
		return fmt.Errorf("dbkit: read the free space of the snapshot folder %s: %w", folder, err)
	}
	if needed := addCapped(used, s.opts.Margin); free < needed {
		return &SpaceError{Folder: folder, Needed: needed, Free: free}
	}
	return nil
}

// addCapped returns a plus b, or math.MaxInt64 when the sum would pass it.
func addCapped(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

// blockBytes returns blocks times size, or math.MaxInt64 when the product would pass it.
func blockBytes(blocks, size uint64) int64 {
	hi, lo := bits.Mul64(blocks, size)
	if hi != 0 || lo > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(lo)
}
