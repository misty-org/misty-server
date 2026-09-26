package browsersync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBrowserSyncRestoreActionsAreRestricted(t *testing.T) {
	refs := map[string]bool{"1": true, "2": true}
	for _, c := range []struct {
		reply string
		ok    bool
	}{
		{`{"type":"type","ref":"1","value":"M"}`, true},
		{`Sure! {"type":"click","ref":"2"} done`, true},
		{`{"type":"click","ref":"9"}`, false},
		{`{"type":"navigate","value":"https://evil.test"}`, false},
		{`{"type":"submit","ref":"1"}`, false},
		{`{"type":"scroll","dy":99999}`, false},
		{`{"type":"done"}`, true},
		{`no json at all`, false},
	} {
		action, parsed := parseRestoreAction(c.reply)
		if got := parsed && validRestoreAction(action, refs); got != c.ok {
			t.Fatalf("%s: got %v want %v", c.reply, got, c.ok)
		}
	}
}

func TestBrowserSyncRestoreCaps(t *testing.T) {
	store, _ := syncTestDatabase(t)
	ctx := context.Background()
	run := uuid.NewString()
	// Steps must arrive in order, and a run stops at the step cap.
	if err := store.consumeRestoreStep(ctx, "owner", run, 1); !errors.Is(err, ErrRestoreLimit) {
		t.Fatalf("step before run start: %v", err)
	}
	for step := 0; step < restoreMaxSteps; step++ {
		if err := store.consumeRestoreStep(ctx, "owner", run, step); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
	if err := store.consumeRestoreStep(ctx, "owner", run, restoreMaxSteps); !errors.Is(err, ErrRestoreLimit) {
		t.Fatalf("step past cap: %v", err)
	}
	// Another account cannot advance this run.
	if err := store.consumeRestoreStep(ctx, "other", run, restoreMaxSteps-1); !errors.Is(err, ErrRestoreLimit) {
		t.Fatalf("cross-account step: %v", err)
	}
	for i := 1; i < restoreMaxRunsPerDay; i++ {
		if err := store.consumeRestoreStep(ctx, "owner", uuid.NewString(), 0); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if err := store.consumeRestoreStep(ctx, "owner", uuid.NewString(), 0); !errors.Is(err, ErrRestoreLimit) {
		t.Fatalf("daily cap: %v", err)
	}
	// Nothing but IDs and counters is stored.
	var columns []string
	rows, err := store.Conn.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name='browser_sync_restore_runs' AND table_schema=current_schema() ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		columns = append(columns, c)
	}
	raw, _ := json.Marshal(columns)
	if string(raw) != `["created_at","restore_id","steps","user_id"]` {
		t.Fatalf("restore runs must hold no payload: %s", raw)
	}
}
