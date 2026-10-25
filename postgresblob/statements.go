// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	// createObjects creates the table of objects.
	createObjects = `CREATE TABLE {objects} (
    id                  uuid PRIMARY KEY,
    key                 bytea NOT NULL,
    state               text NOT NULL CHECK (state IN ('pending', 'live')),
    size                bigint NOT NULL DEFAULT 0,
    chunk_size          integer NOT NULL CHECK (chunk_size > 0),
    content_type        text NOT NULL,
    cache_control       text NOT NULL DEFAULT '',
    content_disposition text NOT NULL DEFAULT '',
    content_encoding    text NOT NULL DEFAULT '',
    content_language    text NOT NULL DEFAULT '',
    metadata            json NOT NULL DEFAULT '{}',
    md5                 bytea,
    created_at          timestamptz NOT NULL DEFAULT now(),
    modified_at         timestamptz NOT NULL DEFAULT now()
)`
	// createLiveKeyIndex lets each key hold one live object at most.
	createLiveKeyIndex = `CREATE UNIQUE INDEX objects_live_key ON {objects} (key) WHERE state = 'live'`
	// createPendingAgeIndex orders the unfinished uploads by age.
	createPendingAgeIndex = `CREATE INDEX objects_pending_age ON {objects} (modified_at) WHERE state = 'pending'`
	// createChunks creates the table of chunks.
	createChunks = `CREATE TABLE {chunks} (
    object_id uuid NOT NULL REFERENCES {objects} (id) ON DELETE CASCADE,
    seq       integer NOT NULL,
    data      bytea NOT NULL,
    PRIMARY KEY (object_id, seq)
)`
	// storeChunksExternal keeps the chunk bytes uncompressed and out of line.
	storeChunksExternal = `ALTER TABLE {chunks} ALTER COLUMN data SET STORAGE EXTERNAL`

	// serverEncoding reads the encoding of the database.
	serverEncoding = `SELECT current_setting('server_encoding')`
	// lockKey takes the transaction lock of one key.
	lockKey = `SELECT pg_advisory_xact_lock($1)`
	// liveExists answers whether a key holds a live object.
	liveExists = `SELECT EXISTS (SELECT FROM {objects} WHERE key = $1 AND state = 'live')`
	// insertPending inserts the pending row of an upload.
	insertPending = `INSERT INTO {objects} (id, key, state, chunk_size, content_type, cache_control,
    content_disposition, content_encoding, content_language, metadata)
VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9)`
	// insertChunk inserts one chunk of a pending upload and marks the upload as touched.
	insertChunk = `WITH touched AS (
    UPDATE {objects} SET modified_at = now() WHERE id = $1 AND state = 'pending' RETURNING id
)
INSERT INTO {chunks} (object_id, seq, data) SELECT id, $2, $3 FROM touched`
	// deleteLive deletes the live object of a key and returns when it was created.
	deleteLive = `DELETE FROM {objects} WHERE key = $1 AND state = 'live' RETURNING created_at`
	// markLive makes a pending row live with its size and hash, keeping the creation time it is given.
	markLive = `UPDATE {objects} SET state = 'live', size = $2, md5 = $3, modified_at = now(),
    created_at = COALESCE($4, created_at)
WHERE id = $1`
	// deletePending deletes the pending row of an upload.
	deletePending = `DELETE FROM {objects} WHERE id = $1 AND state = 'pending'`
	// readAttributes reads the attributes of the live object of a key.
	readAttributes = `SELECT id, size, content_type, cache_control, content_disposition, content_encoding,
    content_language, metadata, md5, created_at, modified_at
FROM {objects} WHERE key = $1 AND state = 'live'`
	// readObject reads what a reader needs of the live object of a key.
	readObject = `SELECT id, size, chunk_size, content_type, modified_at FROM {objects} WHERE key = $1 AND state = 'live'`
	// readRange reads the bytes from $2 up to $3 of an object of chunk size $4, from every chunk the range crosses.
	readRange = `SELECT string_agg(substring(c.data
    FROM (GREATEST(r.start_at, c.seq * r.chunk_size) - c.seq * r.chunk_size + 1)::integer
    FOR (LEAST(r.stop_at, (c.seq + 1) * r.chunk_size) - GREATEST(r.start_at, c.seq * r.chunk_size))::integer),
    ''::bytea ORDER BY c.seq)
FROM {chunks} c, (SELECT $2::bigint AS start_at, $3::bigint AS stop_at, $4::bigint AS chunk_size) r
WHERE c.object_id = $1 AND c.seq BETWEEN r.start_at / r.chunk_size AND (r.stop_at - 1) / r.chunk_size`
	// listLive lists the live objects from a bound up to the end of a prefix, in byte order.
	listLive = `SELECT key, size, md5, modified_at FROM {objects}
WHERE state = 'live' AND key >= GREATEST($1::bytea, $2::bytea) AND key < $3
ORDER BY key LIMIT $4`
	// copySource reads the id, size and hash of the live object of a key.
	copySource = `SELECT id, size, md5 FROM {objects} WHERE key = $1 AND state = 'live'`
	// copyObject inserts a pending copy of an object and of its chunks under a new id and key.
	copyObject = `WITH copied AS (
    INSERT INTO {objects} (id, key, state, size, chunk_size, content_type, cache_control,
        content_disposition, content_encoding, content_language, metadata, md5)
    SELECT $1, $2, 'pending', size, chunk_size, content_type, cache_control,
        content_disposition, content_encoding, content_language, metadata, md5
    FROM {objects} WHERE id = $3
    RETURNING id
)
INSERT INTO {chunks} (object_id, seq, data)
SELECT copied.id, c.seq, c.data FROM copied, {chunks} c WHERE c.object_id = $3`
)

// statements are the SQL statements of one schema.
type statements struct {
	// liveExists answers whether a key holds a live object.
	liveExists string
	// insertPending inserts the pending row of an upload.
	insertPending string
	// insertChunk inserts one chunk of a pending upload.
	insertChunk string
	// deleteLive deletes the live object of a key.
	deleteLive string
	// markLive makes a pending row live.
	markLive string
	// deletePending deletes the pending row of an upload.
	deletePending string
	// readAttributes reads the attributes of a live object.
	readAttributes string
	// readObject reads what a reader needs of a live object.
	readObject string
	// readRange reads a range of an object.
	readRange string
	// listLive lists live objects in byte order.
	listLive string
	// copySource reads the source of a copy.
	copySource string
	// copyObject inserts a pending copy of an object and its chunks.
	copyObject string
}

// tablesOf returns the replacer that writes the quoted names of the tables of schema.
func tablesOf(schema string) *strings.Replacer {
	return strings.NewReplacer(
		"{objects}", pgx.Identifier{schema, "objects"}.Sanitize(),
		"{chunks}", pgx.Identifier{schema, "chunks"}.Sanitize(),
	)
}

// statementsOf returns the statements of the tables of schema.
func statementsOf(schema string) statements {
	tables := tablesOf(schema)
	return statements{
		liveExists:     tables.Replace(liveExists),
		insertPending:  tables.Replace(insertPending),
		insertChunk:    tables.Replace(insertChunk),
		deleteLive:     tables.Replace(deleteLive),
		markLive:       tables.Replace(markLive),
		deletePending:  tables.Replace(deletePending),
		readAttributes: tables.Replace(readAttributes),
		readObject:     tables.Replace(readObject),
		readRange:      tables.Replace(readRange),
		listLive:       tables.Replace(listLive),
		copySource:     tables.Replace(copySource),
		copyObject:     tables.Replace(copyObject),
	}
}

// migrationStatements returns the statements of the first migration of schema.
func migrationStatements(schema string) []string {
	tables := tablesOf(schema)
	return []string{
		tables.Replace(createObjects),
		tables.Replace(createLiveKeyIndex),
		tables.Replace(createPendingAgeIndex),
		tables.Replace(createChunks),
		tables.Replace(storeChunksExternal),
	}
}
