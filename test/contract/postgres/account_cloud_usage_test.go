package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/cloudusage"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)


import ()

func TestAccountCloudGrowthRollsBackAndShrinkSurvivesOutage(t *testing.T) {
	database := openTestDatabase(t)
	t.Setenv("MISTY_BILLING_ADAPTER", "none")
	ctx := context.Background()
	user, e := database.CreateUser("Cloud native facts", "cloud-facts@example.com", "password123")
	if e != nil {
		t.Fatal(e)
	}
	space, e := database.TestingCreateSpace(ctx, user.ID, "Cloud facts")
	if e != nil {
		t.Fatal(e)
	}
	note, e := database.CreateSpaceNote(ctx, user.ID, space.ID, "Original")
	if e != nil {
		t.Fatal(e)
	}
	var admissions atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req billingadapter.Request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid facts")
			w.WriteHeader(400)
			return
		}
		if req.AccountID != user.ID || req.Operation != "storage.commit" || req.Usage.Units["database_bytes"] == 0 {
			t.Error("incorrect native attribution", req)
		}
		admissions.Add(1)
		allowed := req.Usage.Units["database_bytes"] <= 200
		summary, _ := json.Marshal(map[string]any{"limit_bytes": 200, "used_bytes": req.Usage.Units["database_bytes"]})
		if !allowed {
			w.WriteHeader(402)
		}
		json.NewEncoder(w).Encode(billingadapter.Decision{Allowed: allowed, Summary: summary})
	}))
	defer endpoint.Close()
	t.Setenv("MISTY_BILLING_ADAPTER", "http")
	t.Setenv("MISTY_BILLING_URL", endpoint.URL)
	t.Setenv("MISTY_BILLING_SECRET", "cloud-fixture-01234567890123456789")
	t.Setenv("MISTY_ENVIRONMENT", "development")
	update := func(text string) error {
		return database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE space_notes SET markdown_projection=$2 WHERE id=$1`, note.ID, text)
			return err
		})
	}
	if e = update(strings.Repeat("a", 250)); !errors.Is(e, billingadapter.ErrDenied) {
		t.Fatal("growth was not rejected", e)
	}
	var value string
	if e = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT markdown_projection FROM space_notes WHERE id=$1`, note.ID).Scan(&value)
	}); e != nil || value != "" {
		t.Fatal("rejected content committed", value, e)
	}
	if e = update(strings.Repeat("b", 100)); e != nil {
		t.Fatal(e)
	}
	if admissions.Load() != 2 {
		t.Fatal("missing billing admission", admissions.Load())
	}
	endpoint.Close()
	if e = update("small"); e != nil {
		t.Fatal("shrink depends on billing", e)
	}
	// The sync caller's normal RLS context must still inventory this account's
	// retained Space content and restore its previous RLS mode afterwards.
	if e = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','user',true)`); err != nil {
			return err
		}
		facts, err := cloudusage.Facts(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		if facts["database_bytes"] < 5 {
			t.Error("cross-surface inventory omitted note", facts)
		}
		var mode string
		if err = tx.QueryRowContext(ctx, `SELECT current_setting('app.rls_mode')`).Scan(&mode); err != nil {
			return err
		}
		if mode != "user" {
			t.Error("RLS mode escaped inventory", mode)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
