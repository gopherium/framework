// SPDX-License-Identifier: Apache-2.0

package postgresblob_test

import (
	"crypto/rand"
	"net/url"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/gopherium/framework/postgresblob"
)

// sessionBound is how long a test waits for the sessions of a closed pool to end.
const sessionBound = 5 * time.Second

// namedSessions counts the sessions whose application name is its one argument.
const namedSessions = "SELECT count(*) FROM pg_stat_activity WHERE application_name = $1"

// inTestSchema is the query that names testSchema.
var inTestSchema = url.Values{"schema": {testSchema}}

// awaitNoSession fails the test unless no session on the server is named name within sessionBound.
func (db testDB) awaitNoSession(name string) {
	db.t.Helper()
	deadline := time.Now().Add(sessionBound)
	for db.count(namedSessions, name) != 0 {
		if time.Now().After(deadline) {
			db.t.Fatalf("a session named %s stayed %v after Close(), want the pool closed", name, sessionBound)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sessionName returns a new application name no other session has.
func sessionName() string {
	return "postgresblob_" + strings.ToLower(rand.Text())
}

func TestTheURLOpenerOpensTheSchemaTheURLNames(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	const key = "notes/report.txt"

	b, err := blob.OpenBucket(t.Context(), addressWith(t, db.address, inTestSchema))
	if err != nil {
		t.Fatalf("blob.OpenBucket() of a postgres URL = %v, want a bucket", err)
	}
	defer func() { _ = b.Close() }()

	if err := b.WriteAll(t.Context(), key, []byte("text"), nil); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	if n := db.count(liveRows, []byte(key)); n != 1 {
		t.Errorf("%s holds %d live rows of the key, want 1", testSchema, n)
	}
}

func TestTheURLOpenerRefusesAURLPgxCannotParseWithAFixedMessage(t *testing.T) {
	t.Parallel()

	b, err := blob.OpenBucket(t.Context(), "postgres://reader:hunter2@db.example.com/files?sslmode=sometimes")

	if err == nil || mentionsAny(err, "db.example.com", "hunter2") {
		t.Errorf("blob.OpenBucket() of a URL with a bad sslmode = %v, %v, want an error that holds no part of the URL",
			b, err)
	}
}

func TestTheURLOpenerClosesItsPoolWhenTheDatabaseIsRefused(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	name := sessionName()

	b, err := blob.OpenBucket(t.Context(), addressWith(t, asciiDatabase(t), url.Values{"application_name": {name}}))

	if !mentions(err, "UTF8") {
		t.Errorf("blob.OpenBucket() of a SQL_ASCII database = %v, %v, want an error that names UTF8", b, err)
	}
	db.awaitNoSession(name)
}

func TestTheURLOpenerGivesEachBucketItsOptions(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	mux := new(blob.URLMux)
	mux.RegisterBucket(postgresblob.Scheme, &postgresblob.URLOpener{Options: postgresblob.Options{MaxSize: 4}})
	b, err := mux.OpenBucket(t.Context(), addressWith(t, db.address, inTestSchema))
	if err != nil {
		t.Fatalf("OpenBucket() of a postgres URL = %v, want a bucket", err)
	}
	defer func() { _ = b.Close() }()

	err = b.WriteAll(t.Context(), "notes/report.txt", []byte("five!"), textOptions)

	expectCode(t, "WriteAll() of 5 bytes past the cap of 4", err, gcerrors.ResourceExhausted)
}

func TestTheURLOpenerClosesThePoolItOpened(t *testing.T) {
	t.Parallel()
	db := freshDB(t)
	name := sessionName()
	address := addressWith(t, db.address, url.Values{"schema": {testSchema}, "application_name": {name}})
	b, err := blob.OpenBucket(t.Context(), address)
	if err != nil {
		t.Fatalf("blob.OpenBucket() of a postgres URL = %v, want a bucket", err)
	}
	if err := b.WriteAll(t.Context(), "notes/report.txt", []byte("text"), nil); err != nil {
		t.Fatalf("WriteAll() = %v, want nil", err)
	}
	if db.count(namedSessions, name) == 0 {
		t.Fatalf("no session is named %s after a write, want the pool of the opener on the server", name)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	db.awaitNoSession(name)
}
