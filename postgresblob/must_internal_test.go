// SPDX-License-Identifier: Apache-2.0

package postgresblob

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMustPanicsOnTheErrorOfAPoolWithNoRoom(t *testing.T) {
	t.Parallel()

	config, err := pgxpool.ParseConfig("postgres://reader@127.0.0.1:1/files")
	if err != nil {
		t.Fatalf("ParseConfig() = %v, want a configuration", err)
	}
	config.MaxConns = 0
	defer func() {
		if recover() == nil {
			t.Error("must() returned, want a panic with the error of a pool capped at zero")
		}
	}()

	must(pgxpool.NewWithConfig(context.Background(), config))
}
