// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mustKept is a value must hands back unchanged.
const mustKept = 2

func TestMustPanicsOnTheErrorOfAPoolWithNoRoom(t *testing.T) {
	t.Parallel()

	config, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/x")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v, want nil", err)
	}
	config.MaxConns = 0
	defer func() {
		if r := recover(); r == nil {
			t.Error("must() returned, want a panic with the error of a pool capped at zero")
		}
	}()

	must(pgxpool.NewWithConfig(context.Background(), config))
}

func TestMustReturnsTheValueWithNoError(t *testing.T) {
	t.Parallel()

	if got := must(mustKept, nil); got != mustKept {
		t.Errorf("must() = %d, want %d", got, mustKept)
	}
}
