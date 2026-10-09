// SPDX-License-Identifier: Apache-2.0

package exampleapp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	"github.com/gopherium/framework/gonsole"
)

// errConnected is the error of a connection the example handle never makes.
var errConnected = errors.New("exampleapp: the example handle never connects")

// HandleProgram returns the example program that migrates on one database handle, whose settings getenv reads.
func HandleProgram(getenv func(string) string) gonsole.Program {
	return gonsole.Program{
		Name:     "myapp",
		Env:      gonsole.Env{Prefix: "MYAPP_", Getenv: getenv},
		Database: "DATABASE_URL",
		Open:     open,
		Validate: validate,
		Migrations: []gonsole.Step{
			{Name: "accounts", Run: func(context.Context, string) error { return nil }},
			{Name: "reports", RunOn: reports},
		},
	}
}

// open returns a handle over a connector that never connects.
func open(context.Context, string) (*sql.DB, error) {
	return sql.OpenDB(offline{}), nil
}

// validate asks for the run's handle, which opens without a connection.
func validate(ctx context.Context, call gonsole.Call) error {
	_, err := call.DB(ctx)
	return err
}

// reports applies the reports schema on the run's handle.
func reports(context.Context, *sql.DB) error {
	return nil
}

// offline is a connector whose every connection fails.
type offline struct{}

// Connect refuses the connection.
func (offline) Connect(context.Context) (driver.Conn, error) {
	return nil, errConnected
}

// Driver answers the connector's driver.
func (offline) Driver() driver.Driver {
	return offlineDriver{}
}

// offlineDriver is the driver of an offline connector.
type offlineDriver struct{}

// Open refuses the connection.
func (offlineDriver) Open(string) (driver.Conn, error) {
	return nil, errConnected
}
