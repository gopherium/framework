// SPDX-License-Identifier: Apache-2.0

package dbkit

import "strconv"

// Engine names the database engine an address selects.
type Engine int

const (
	// Postgres is PostgreSQL.
	Postgres Engine = iota + 1
	// SQLite is SQLite in one file.
	SQLite
)

// String returns the engine's name.
func (e Engine) String() string {
	switch e {
	case Postgres:
		return "postgres"
	case SQLite:
		return "sqlite"
	default:
		return "Engine(" + strconv.Itoa(int(e)) + ")"
	}
}
