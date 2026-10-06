// SPDX-License-Identifier: Apache-2.0

package dbkit

import (
	"context"
	"fmt"
	"maps"
	"sync"
)

// QueryBudget hands each request a fresh statement counter with the same limit.
type QueryBudget struct {
	limit int
}

// NewQueryBudget returns a budget of limit statements per request.
func NewQueryBudget(limit int) (*QueryBudget, error) {
	if limit < 1 {
		return nil, fmt.Errorf("dbkit: the query budget limit must be 1 or more, got %d", limit)
	}
	return &QueryBudget{limit: limit}, nil
}

// Attach returns a copy of ctx that carries a fresh statement counter.
func (b *QueryBudget) Attach(ctx context.Context) context.Context {
	return context.WithValue(ctx, counterKey{}, &counter{limit: b.limit, byID: map[string]int{}})
}

// counterKey is the context key of the statement counter of a request.
type counterKey struct{}

// counter is the statement counter of one request, safe for statements at once.
type counter struct {
	mu    sync.Mutex
	limit int
	total int
	byID  map[string]int
}

// countStatement adds one statement of the share id to the counter in ctx and returns the total and the budget mark.
func countStatement(ctx context.Context, id string) (int, bool) {
	c, ok := ctx.Value(counterKey{}).(*counter)
	if !ok {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	c.byID[id]++
	return c.total, c.total > c.limit
}

// currentCount returns the total of the counter in ctx and its budget mark without adding to it.
func currentCount(ctx context.Context) (int, bool) {
	c, ok := ctx.Value(counterKey{}).(*counter)
	if !ok {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total, c.total > c.limit
}

// QueryCount is a copy of the statement counter of one request.
type QueryCount struct {
	// Total is the number of statements of every share.
	Total int
	// ByID is the number of statements of each share, keyed by its ID.
	ByID map[string]int
	// PastBudget reports whether Total is above the limit of the budget.
	PastBudget bool
}

// QueriesIn returns a copy of the statement counter in ctx, or false when ctx carries none.
func QueriesIn(ctx context.Context) (QueryCount, bool) {
	c, ok := ctx.Value(counterKey{}).(*counter)
	if !ok {
		return QueryCount{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return QueryCount{Total: c.total, ByID: maps.Clone(c.byID), PastBudget: c.total > c.limit}, true
}
