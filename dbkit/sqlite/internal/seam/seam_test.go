// SPDX-License-Identifier: Apache-2.0

package seam_test

import (
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/gopherium/framework/dbkit/sqlite/internal/seam"
)

// seamConn is a connection that does nothing.
type seamConn struct {
	// label tells one connection from another.
	label string
}

// Prepare refuses every statement.
func (c *seamConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("seam test: no statement runs on a seamConn")
}

// Close closes nothing.
func (c *seamConn) Close() error {
	return nil
}

// Begin refuses every transaction.
func (c *seamConn) Begin() (driver.Tx, error) {
	return nil, errors.New("seam test: no transaction runs on a seamConn")
}

// seamWrapped is a connection a wrapper returned around inner.
type seamWrapped struct {
	seamConn
	// inner is the connection the wrapper received.
	inner driver.Conn
}

// seamWrapper returns a wrapper that marks every connection with label.
func seamWrapper(label string) func(driver.Conn) driver.Conn {
	return func(c driver.Conn) driver.Conn {
		return &seamWrapped{seamConn: seamConn{label: label}, inner: c}
	}
}

// seamPath returns a fresh absolute path no other test registers.
func seamPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "site.db")
}

// seamMustWrap fails the test unless got is a wrapped connection with label around inner.
func seamMustWrap(t *testing.T, got driver.Conn, label string, inner driver.Conn) {
	t.Helper()
	wrapped, ok := got.(*seamWrapped)
	if !ok {
		t.Fatalf("Wrap() = %T, want the wrapper's connection", got)
	}
	if wrapped.label != label || wrapped.inner != inner {
		t.Errorf("Wrap() = wrapper %q around %p, want wrapper %q around %p", wrapped.label, wrapped.inner, label, inner)
	}
}

func TestWrapWithNoWrapperReturnsTheConnection(t *testing.T) {
	t.Parallel()

	conn := &seamConn{label: "plain"}

	if got := seam.Wrap(seamPath(t), conn); got != conn {
		t.Errorf("Wrap() = %v, want the connection unchanged", got)
	}
}

func TestWrapPassesTheConnectionThroughItsPathsWrapper(t *testing.T) {
	t.Parallel()

	path := seamPath(t)
	conn := &seamConn{label: "plain"}
	seam.Set(path, seamWrapper("set"))
	t.Cleanup(func() { seam.Clear(path) })

	seamMustWrap(t, seam.Wrap(path, conn), "set", conn)
}

func TestWrapLeavesOtherPathsAlone(t *testing.T) {
	t.Parallel()

	path, other := seamPath(t), seamPath(t)
	conn := &seamConn{label: "plain"}
	seam.Set(path, seamWrapper("set"))
	t.Cleanup(func() { seam.Clear(path) })

	if got := seam.Wrap(other, conn); got != conn {
		t.Errorf("Wrap() on another path = %v, want the connection unchanged", got)
	}
}

func TestPathsMatchOnceCleaned(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	path := filepath.Join(folder, "site.db")
	unclean := folder + "/./nested/../site.db"
	conn := &seamConn{label: "plain"}
	seam.Set(unclean, seamWrapper("unclean"))
	t.Cleanup(func() { seam.Clear(path) })

	seamMustWrap(t, seam.Wrap(path, conn), "unclean", conn)
}

func TestSetReplacesTheWrapper(t *testing.T) {
	t.Parallel()

	path := seamPath(t)
	conn := &seamConn{label: "plain"}
	seam.Set(path, seamWrapper("first"))
	seam.Set(path, seamWrapper("second"))
	t.Cleanup(func() { seam.Clear(path) })

	seamMustWrap(t, seam.Wrap(path, conn), "second", conn)
}

func TestClearRemovesTheWrapper(t *testing.T) {
	t.Parallel()

	path := seamPath(t)
	conn := &seamConn{label: "plain"}
	seam.Set(path, seamWrapper("set"))

	seam.Clear(path + "/.")

	if got := seam.Wrap(path, conn); got != conn {
		t.Errorf("Wrap() after Clear() = %v, want the connection unchanged", got)
	}
}

func TestSeamServesManyPathsAtOnce(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			path := filepath.Join(folder, strconv.Itoa(i)+".db")
			label := strconv.Itoa(i)
			conn := &seamConn{label: "plain"}
			seam.Set(path, seamWrapper(label))
			got, ok := seam.Wrap(path, conn).(*seamWrapped)
			seam.Clear(path)
			if !ok || got.label != label {
				t.Errorf("Wrap() on path %d = %v, want the wrapper %q", i, got, label)
			}
		})
	}
	wg.Wait()
}
