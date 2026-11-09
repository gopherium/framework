// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// internalFingerprintOpen returns a handle on a fresh file with the tables fp_a and fp_b, one row each.
func internalFingerprintOpen(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open("sqlite:"+filepath.Join(internalFolder(t), "site.db"), internalOptions())
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		"CREATE TABLE fp_a (a INTEGER NOT NULL)",
		"CREATE TABLE fp_b (b INTEGER NOT NULL)",
		"INSERT INTO fp_a VALUES (1)",
		"INSERT INTO fp_b VALUES (1)",
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatalf("ExecContext(%q) error = %v, want nil", query, err)
		}
	}
	return db
}

// internalAfterTable puts in place of walked one that runs then once, after the table first is read.
func internalAfterTable(t *testing.T, first string, then func()) {
	t.Helper()
	kept := walked
	walked = func(table string) {
		if table == first {
			then()
		}
	}
	t.Cleanup(func() { walked = kept })
}

// internalFingerprint returns the fingerprint of db and fails the test when there is none.
func internalFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	fp, err := Fingerprint(t.Context(), db, Leave{})
	if err != nil {
		t.Fatalf("Fingerprint() error = %v, want nil", err)
	}
	return fp
}

func TestAWriterCommitsDuringTheReadWithNoWait(t *testing.T) {
	db := internalFingerprintOpen(t)
	before := internalFingerprint(t, db)
	var wrote error
	internalAfterTable(t, "fp_a", func() {
		ctx, cancel := context.WithTimeout(t.Context(), internalCheckpointDeadline)
		defer cancel()
		_, wrote = db.ExecContext(ctx, "INSERT INTO fp_b VALUES (2)")
	})

	during := internalFingerprint(t, db)

	if wrote != nil {
		t.Errorf("the write during the read error = %v, want it committed within %v", wrote, internalCheckpointDeadline)
	}
	if during != before {
		t.Errorf("Fingerprint() with a write during the read = %s, want %s from the start of the read", during, before)
	}
	if after := internalFingerprint(t, db); after == before {
		t.Errorf("Fingerprint() after the write = %s, want it moved from %s", after, before)
	}
}

func TestAContextEndingDuringTheReadFails(t *testing.T) {
	db := internalFingerprintOpen(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	internalAfterTable(t, "fp_a", cancel)

	fp, err := Fingerprint(ctx, db, Leave{})

	if !errors.Is(err, context.Canceled) || fp != "" {
		t.Errorf("Fingerprint() = %q, %v, want no fingerprint and an error matching context.Canceled", fp, err)
	}
}

func TestTwoTextsKeepTheirLengths(t *testing.T) {
	t.Parallel()

	first, second := sha256.New(), sha256.New()
	writeText(first, "ab")
	writeText(first, "c")
	writeText(second, "a")
	writeText(second, "bc")

	if a, b := first.Sum(nil), second.Sum(nil); bytes.Equal(a, b) {
		t.Errorf("the hash of the texts ab and c = %x, want it apart from the hash of a and bc", a)
	}
}

func TestEachRowAnswersAScanError(t *testing.T) {
	db := internalFingerprintOpen(t)
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var a, b int64

	err = fingerprintRead{tx: tx}.eachRow(t.Context(), statement{text: "SELECT a FROM fp_a"}, []any{&a, &b}, func() {})

	if err == nil || !strings.Contains(err.Error(), "destination arguments") {
		t.Errorf("eachRow() error = %v, want the scan error of two destinations for one column", err)
	}
}
