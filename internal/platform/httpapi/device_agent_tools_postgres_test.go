package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestDeviceAgentToolsPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	database := testkit.OpenDatabase(t)
	// Device jobs wait on account events, whose listener connects with DB_*.
	for _, name := range []string{"HOST", "PORT", "USER", "PASSWORD", "NAME", "SSLMODE"} {
		t.Setenv("DB_"+name, os.Getenv("TEST_DB_"+name))
	}
	ctx := t.Context()
	owner, _ := database.CreateUser("Owner", "device-tools-owner@example.test", "password123")
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(owner.ID, "Studio Mac", base64.RawURLEncoding.EncodeToString(publicKey), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_device_tools", UserID: owner.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: "device-tools", RequestPayload: json.RawMessage(`{"prompt":"summarize my reports"}`), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	attach := func(kind, ref, name, capabilities string) error {
		_, err := database.AttachAIInvocationContext(ctx, owner.ID, record.ID, "", device.ID, kind, ref, name, json.RawMessage(capabilities), nil)
		return err
	}
	if err := attach("local_folder", "scope_reports", "Reports", `["files.list","files.read"]`); err != nil {
		t.Fatal(err)
	}
	if err := attach("workspace", "workspace:misty-browser", "Misty browser", `["tabs.list","tabs.open","bookmarks.list","bookmarks.add"]`); err != nil {
		t.Fatal(err)
	}
	if err := attach("local_folder", "scope_bad", "Bad", `["browser.navigate"]`); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("a folder grant asked for browser control: %v", err)
	}
	if err := attach("workspace", "workspace:bad", "Bad", `["files.read"]`); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("a workspace grant asked for files: %v", err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// The desktop is online: it heartbeats while the app is open.
		if _, err := tx.ExecContext(ctx, `UPDATE trusted_devices SET last_seen_at=NOW() WHERE id=$1`, device.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running' WHERE id=$1`, record.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	service := &SpacesService{database: database}
	tools := map[string]agenttools.Handler{}
	for _, registration := range service.deviceToolRegistrations(ctx, &record) {
		tools[registration.Descriptor.Name] = registration.Handler
	}
	for _, name := range []string{filesListTool, filesReadTool, tabsListTool, tabsOpenTool, bookmarksListTool, bookmarksAddTool} {
		if tools[name] == nil {
			t.Fatalf("chat with folder and workspace grants lacks %s", name)
		}
	}
	invocation := agenttools.Invocation{UserID: owner.ID, RunID: record.ID}
	var invalid agent.ErrInvalidRequest
	if _, err := tools[filesReadTool](ctx, invocation, agent.ToolRequest{ID: "c0", Name: filesReadTool, Arguments: json.RawMessage(`{"folder":"Taxes","path":"a.pdf"}`)}); !errors.As(err, &invalid) {
		t.Fatalf("unshared folder = %v", err)
	}

	// The desktop finishes the job; the tool returns its output.
	done := make(chan json.RawMessage, 1)
	go func() {
		raw, err := tools[filesReadTool](ctx, invocation, agent.ToolRequest{ID: "c1", Name: filesReadTool, Arguments: json.RawMessage(`{"folder":"Reports","path":"q3.txt"}`)})
		if err != nil {
			t.Error(err)
		}
		done <- raw
	}()
	var jobID string
	var input, operation string
	for deadline := time.Now().Add(10 * time.Second); jobID == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		_ = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT id,input::text,operation FROM workflow_device_node_jobs WHERE invocation_id=$1`, record.ID).Scan(&jobID, &input, &operation)
		})
	}
	if jobID == "" || operation != filesReadTool || !strings.Contains(input, `"scopeId": "scope_reports"`) && !strings.Contains(input, `"scopeId":"scope_reports"`) {
		t.Fatalf("queued job = %q %q %q", jobID, operation, input)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE workflow_device_node_jobs SET state='completed',output='{"text":"Revenue grew"}',completed_at=NOW() WHERE id=$1`, jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-done:
		if !strings.Contains(string(raw), "Revenue grew") {
			t.Fatalf("files.read = %s", raw)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("files.read did not return after the device finished")
	}

	// A chat without grants gets no device tools.
	plain, _, _ := database.CreateAIInvocationRecord(context.Background(), db.AIInvocationRecord{
		ID: "invocation_no_grants", UserID: owner.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "running",
		IdempotencyKey: "no-grants", RequestPayload: json.RawMessage(`{"prompt":"hi"}`), ExpiresAt: time.Now().Add(time.Hour),
	})
	if registrations := service.deviceToolRegistrations(ctx, &plain); len(registrations) != 0 {
		t.Fatalf("chat without grants got %d device tools", len(registrations))
	}
	if err := TestingValidateAIInvocationDeviceContexts([]byte(`[]`), []byte(`[{"device_id":"d","kind":"local_folder","opaque_ref":"s","capabilities":["files.read"]}]`), ""); err != nil {
		t.Fatalf("folder grant without a page reference = %v", err)
	}
	if err := TestingValidateAIInvocationDeviceContexts([]byte(`[]`), []byte(`[{"device_id":"d","kind":"local_folder","opaque_ref":"s","capabilities":["files.delete"]}]`), ""); err == nil {
		t.Fatal("a folder grant asked to delete files")
	}
}
