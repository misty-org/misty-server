package app

import (
	"context"
	"errors"
	"testing"
)

func TestRetentionPassDrainsFullBatchesWithinBudget(t *testing.T) {
	backlog := 45
	drains, capped, once, failing := 0, 0, 0, 0
	tasks := []retentionTask{
		{"drain", 10, func(context.Context) (int, error) {
			drains++
			n := min(backlog, 10)
			backlog -= n
			return n, nil
		}},
		{"capped", 1, func(context.Context) (int, error) { capped++; return 1, nil }},
		{"failing", 10, func(context.Context) (int, error) { failing++; return 10, errors.New("boom") }},
		{"once", 0, func(context.Context) (int, error) { once++; return 0, nil }},
	}
	err := runRetentionPass(context.Background(), tasks)
	// 45 rows: four full batches, then a short one ends the drain.
	if drains != 5 || backlog != 0 {
		t.Fatal("drain", drains, backlog)
	}
	if capped != retentionBatches {
		t.Fatal("an endless backlog must stop at the batch budget", capped)
	}
	if failing != 1 || err == nil {
		t.Fatal("a failing task must stop and be reported", failing, err)
	}
	if once != 1 {
		t.Fatal("unbatched task must run once", once)
	}
}
