// SPDX-License-Identifier: Apache-2.0

package gonsole_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/gopherium/framework/gonsole"
)

// handleActor is the account the guarded handle tests act as.
const handleActor = "admin@example.com"

// counter is a connector whose connections it counts, its Close answering closeFails.
type counter struct {
	// connects counts the connections the connector made.
	connects atomic.Int32
	// closeFails is the error Close answers, nil for none.
	closeFails error
}

// Connect counts one connection and answers it.
func (c *counter) Connect(context.Context) (driver.Conn, error) {
	c.connects.Add(1)
	return idle{}, nil
}

// Driver answers the driver of the connector.
func (c *counter) Driver() driver.Driver {
	return counting{connector: c}
}

// Close answers closeFails.
func (c *counter) Close() error {
	return c.closeFails
}

// counting is the driver of a counter.
type counting struct {
	// connector is the counter whose connections the driver makes.
	connector *counter
}

// Open makes one connection through the counter.
func (d counting) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

// idle is a connection that runs no statement.
type idle struct{}

// Prepare refuses every statement.
func (idle) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("handle test: the connection runs no statement")
}

// Close closes nothing.
func (idle) Close() error {
	return nil
}

// Begin refuses every transaction.
func (idle) Begin() (driver.Tx, error) {
	return nil, errors.New("handle test: the connection begins no transaction")
}

// opener is a Program.Open that counts its opens and answers a handle over its counter, or fails.
type opener struct {
	// connector is the counter every handle the opener answers runs over.
	connector *counter
	// fails is the error every open answers, nil for none.
	fails error
	// empty makes every open answer no handle and no error.
	empty bool
	// opens counts the opens.
	opens atomic.Int32
	// address is the database address the last open received.
	address string
	// live reports whether the last open received a context that had not ended.
	live bool
}

// open counts one open and answers a handle over the counter, the failure when the opener fails.
func (o *opener) open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	o.opens.Add(1)
	o.address, o.live = databaseURL, ctx.Err() == nil
	switch {
	case o.fails != nil:
		return nil, o.fails
	case o.empty:
		return nil, nil
	}
	return sql.OpenDB(o.connector), nil
}

// newOpener returns an opener over a fresh counter.
func newOpener() *opener {
	return &opener{connector: &counter{}}
}

// handles records every handle the parts of one run receive, in the order they ask.
type handles struct {
	// seen are the handles in the order the parts asked.
	seen []*sql.DB
	// failures are the errors in the order the parts asked.
	failures []error
}

// ask asks call for its handle, records the answer and returns it.
func (h *handles) ask(ctx context.Context, call gonsole.Call) (*sql.DB, error) {
	db, err := call.DB(ctx)
	h.seen, h.failures = append(h.seen, db), append(h.failures, err)
	return db, err
}

// same reports whether every recorded handle is the one non-nil handle.
func (h *handles) same() bool {
	return len(h.seen) > 0 && h.seen[0] != nil && !slices.ContainsFunc(h.seen, func(db *sql.DB) bool {
		return db != h.seen[0]
	})
}

// handled returns a program called myapp over the database setting whose opener is o.
func handled(o *opener, commands ...gonsole.Command) gonsole.Program {
	return gonsole.Program{
		Name:     "myapp",
		Env:      settings(map[string]string{"MYAPP_PRIMARY_URL": databaseAddress}),
		Database: "PRIMARY_URL",
		Open:     o.open,
		Commands: commands,
	}
}

// asking returns a command called report:list whose run asks for the handle through h.
func asking(h *handles) gonsole.Command {
	return gonsole.Command{
		Name: "report:list", Summary: "list every report",
		Run: func(ctx context.Context, call gonsole.Call) error {
			_, err := h.ask(ctx, call)
			return err
		},
	}
}

// pluggedHandle registers one plugin group whose registration and command ask for the handle through h.
func pluggedHandle(
	h *handles, release func(ctx context.Context) error,
) func(context.Context, gonsole.Call) (gonsole.Loaded, error) {
	return func(ctx context.Context, call gonsole.Call) (gonsole.Loaded, error) {
		if _, err := h.ask(ctx, call); err != nil && !errors.Is(err, gonsole.ErrDescribing) {
			return gonsole.Loaded{}, err
		}
		count := gonsole.Command{
			Name: "demo:count", Summary: "count the demo rows",
			Run: func(ctx context.Context, call gonsole.Call) error {
				_, err := h.ask(ctx, call)
				return err
			},
		}
		return gonsole.Loaded{
			Groups:  []gonsole.Group{{Namespace: "demo", Commands: []gonsole.Command{count}}},
			Release: release,
		}, nil
	}
}

func TestThePluginsAndTheCommandShareOneHandle(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	p := handled(o)
	p.Plugins = pluggedHandle(&h, nil)

	got := execute(t, p, "demo:count")

	if got.code != gonsole.ExitDone || len(h.seen) != 2 || !h.same() {
		t.Errorf("code %d, handles %v, want 0 and one handle for the registration and the command, stderr %q",
			got.code, h.seen, got.stderr)
	}
	if opens := o.opens.Load(); opens != 1 || o.address != databaseAddress {
		t.Errorf("Open ran %d times at %q, want once at %q", opens, o.address, databaseAddress)
	}
}

func TestAuthorizeAndRecordShareTheHandle(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	writing := asking(&h)
	writing.Name, writing.Writes, writing.Capability = "report:create", true, "report.write"
	p := handled(o, writing)
	p.Authorize = func(ctx context.Context, call gonsole.Call, _ string) error {
		_, err := h.ask(ctx, call)
		return err
	}
	p.Record = func(ctx context.Context, call gonsole.Call, _ string) error {
		_, err := h.ask(ctx, call)
		return err
	}

	got := execute(t, p, "report:create", "-yes", "-as", handleActor)

	if got.code != gonsole.ExitDone || len(h.seen) != 3 || !h.same() {
		t.Errorf("code %d, handles %v, want 0 and one handle for Authorize, the command and Record, stderr %q",
			got.code, h.seen, got.stderr)
	}
	if opens := o.opens.Load(); opens != 1 {
		t.Errorf("Open ran %d times, want once", opens)
	}
}

func TestRunClosesTheHandleAfterThePluginsRelease(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	var released error
	p := handled(o)
	p.Plugins = pluggedHandle(&h, func(ctx context.Context) error {
		released = errors.New("handle test: the release found no handle")
		if h.same() {
			released = h.seen[0].PingContext(ctx)
		}
		return nil
	})

	got := execute(t, p, "demo:count")

	if got.code != gonsole.ExitDone || released != nil || !h.same() {
		t.Fatalf("code %d, the release reached the handle with %v, want 0 and nil, stderr %q",
			got.code, released, got.stderr)
	}
	if err := h.seen[0].PingContext(t.Context()); errorText(err) != "sql: database is closed" {
		t.Errorf("PingContext() after the run = %v, want sql: database is closed", err)
	}
}

func TestACancelledFirstCallerLeavesTheHandleUsable(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	p := handled(o, gonsole.Command{
		Name: "report:list", Summary: "list every report",
		Run: func(ctx context.Context, call gonsole.Call) error {
			ended, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := h.ask(ended, call); err != nil {
				return err
			}
			db, err := h.ask(ctx, call)
			if err != nil {
				return err
			}
			return db.PingContext(ctx)
		},
	})

	got := execute(t, p, "report:list")

	if got.code != gonsole.ExitDone || len(h.seen) != 2 || !h.same() || !o.live {
		t.Errorf("code %d, handles %v, open saw a live context %t, want 0, one handle and true, stderr %q",
			got.code, h.seen, o.live, got.stderr)
	}
}

func TestAFailedOpenIsReturnedToEveryCaller(t *testing.T) {
	t.Parallel()

	o := newOpener()
	o.fails = errors.New("unable to open database file")
	var h handles
	p := handled(o, gonsole.Command{
		Name: "report:list", Summary: "list every report",
		Run: func(ctx context.Context, call gonsole.Call) error {
			_, first := h.ask(ctx, call)
			_, second := h.ask(ctx, call)
			return errors.Join(first, second)
		},
	})

	execute(t, p, "report:list")

	if len(h.failures) != 2 || !errors.Is(h.failures[0], o.fails) || h.failures[0] != h.failures[1] {
		t.Errorf("DB() errors = %v, want the one open failure twice", h.failures)
	}
	if opens := o.opens.Load(); opens != 1 {
		t.Errorf("Open ran %d times, want once", opens)
	}
}

func TestOpenFailureIsWrapped(t *testing.T) {
	t.Parallel()

	o := newOpener()
	o.fails = errors.New("unable to open database file")
	var h handles

	got := execute(t, handled(o, asking(&h)), "report:list")

	if want := "myapp: open the database: unable to open database file\n"; got.code != gonsole.ExitFailed ||
		got.stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.code, got.stderr, want)
	}
	if !errors.Is(h.failures[0], o.fails) {
		t.Errorf("DB() error = %v, want the open failure wrapped", h.failures[0])
	}
}

func TestDescribeRefusesTheHandle(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"list"}, {"help", "demo:count"}} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()

			o := newOpener()
			var h handles
			p := handled(o)
			p.Plugins = pluggedHandle(&h, nil)

			got := execute(t, p, args...)

			if got.code != gonsole.ExitDone || len(h.failures) != 1 || !errors.Is(h.failures[0], gonsole.ErrDescribing) {
				t.Errorf("code %d, DB() errors %v, want 0 and ErrDescribing, stderr %q", got.code, h.failures, got.stderr)
			}
			if opens := o.opens.Load(); opens != 0 {
				t.Errorf("Open ran %d times, want never", opens)
			}
		})
	}
}

func TestCheckMakesNoDatabaseConnection(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	p := handled(o)
	p.Plugins = pluggedHandle(&h, nil)
	p.Validate = func(ctx context.Context, call gonsole.Call) error {
		_, err := h.ask(ctx, call)
		return err
	}

	got := execute(t, p, "check")

	if got.code != gonsole.ExitDone || len(h.seen) != 2 || !h.same() {
		t.Errorf("code %d, handles %v, want 0 and one handle for Validate and the plugins, stderr %q",
			got.code, h.seen, got.stderr)
	}
	if connects := o.connector.connects.Load(); connects != 0 {
		t.Errorf("check made %d database connections, want none", connects)
	}
}

func TestARunThatNeverAsksOpensNothing(t *testing.T) {
	t.Parallel()

	o := newOpener()

	got := execute(t, handled(o, answering(nil)), "report:list")

	if got.code != gonsole.ExitDone || o.opens.Load() != 0 {
		t.Errorf("code %d, Open ran %d times, want 0 and never, stderr %q", got.code, o.opens.Load(), got.stderr)
	}
}

func TestRunJoinsTheCloseFailure(t *testing.T) {
	t.Parallel()

	o := newOpener()
	o.connector.closeFails = errors.New("disk I/O error")
	var h handles

	got := execute(t, handled(o, asking(&h)), "report:list")

	if want := "myapp: close the database: disk I/O error\n"; got.code != gonsole.ExitFailed || got.stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.code, got.stderr, want)
	}
}

func TestDBRefusesAnOpenThatAnswersNoHandle(t *testing.T) {
	t.Parallel()

	o := newOpener()
	o.empty = true
	var h handles

	got := execute(t, handled(o, asking(&h)), "report:list")

	if want := "myapp: gonsole: Open answered no database\n"; got.code != gonsole.ExitFailed || got.stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.code, got.stderr, want)
	}
}

func TestDBRefusesAProgramWithoutOpen(t *testing.T) {
	t.Parallel()

	var h handles
	p := handled(newOpener(), asking(&h))
	p.Open = nil

	got := execute(t, p, "report:list")

	if want := "myapp: gonsole: the program has no Open\n"; got.code != gonsole.ExitFailed || got.stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.code, got.stderr, want)
	}
}

func TestDBRefusesAMissingDatabaseSetting(t *testing.T) {
	t.Parallel()

	o := newOpener()
	var h handles
	p := handled(o, asking(&h))
	p.Env = settings(nil)

	got := execute(t, p, "report:list")

	if want := "myapp: MYAPP_PRIMARY_URL is required\n"; got.code != gonsole.ExitFailed || got.stderr != want {
		t.Errorf("code %d, stderr %q, want 1 and %q", got.code, got.stderr, want)
	}
	if opens := o.opens.Load(); opens != 0 {
		t.Errorf("Open ran %d times, want never", opens)
	}
}

func TestDBNeedsACallTheEngineBuilt(t *testing.T) {
	t.Parallel()

	_, err := gonsole.Call{}.DB(t.Context())

	if want := "gonsole: no database handle in this call"; errorText(err) != want {
		t.Errorf("DB() error = %q, want %q", errorText(err), want)
	}
}

func TestWithDBCarriesAReadyHandle(t *testing.T) {
	t.Parallel()

	db := sql.OpenDB(&counter{})
	t.Cleanup(func() { _ = db.Close() })

	got, err := gonsole.Call{}.WithDB(db).DB(t.Context())

	if got != db || err != nil {
		t.Errorf("DB() = %p, %v, want %p and nil", got, err, db)
	}
}
