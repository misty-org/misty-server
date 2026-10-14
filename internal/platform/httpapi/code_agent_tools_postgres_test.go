package api

import (
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

// A chat with a project grant gets the code tools; each call becomes a device
// job for that project's scope, and the device's result is the tool's result
// (docs/plans/agent-code-projects.md).
func TestCodeProjectToolsPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	database := testkit.OpenDatabase(t)
	for _, name := range []string{"HOST", "PORT", "USER", "PASSWORD", "NAME", "SSLMODE"} {
		t.Setenv("DB_"+name, os.Getenv("TEST_DB_"+name))
	}
	ctx := t.Context()
	owner, _ := database.CreateUser("Owner", "code-projects-owner@example.test", "password123")
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(owner.ID, "Studio Mac", base64.RawURLEncoding.EncodeToString(publicKey), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_code_projects", UserID: owner.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: "code-projects", RequestPayload: json.RawMessage(`{"prompt":"build my site"}`), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	attach := func(kind, ref, name, capabilities string) error {
		_, err := database.AttachAIInvocationContext(ctx, owner.ID, record.ID, "", device.ID, kind, ref, name, json.RawMessage(capabilities), nil)
		return err
	}
	if err := attach("project_folder", "scope_site", "site", `["code.read","code.write","code.edit","code.run","code.preview"]`); err != nil {
		t.Fatalf("project grant: %v", err)
	}
	for name, capabilities := range map[string]string{
		"file deletes":    `["files.delete"]`,
		"browser control": `["code.run","browser.navigate"]`,
	} {
		if err := attach("project_folder", "scope_bad_"+strings.ReplaceAll(name, " ", "_"), "Bad", capabilities); !errors.Is(err, db.ErrSpaceInvalid) {
			t.Fatalf("a project grant asked for %s: %v", name, err)
		}
	}
	if err := attach("local_folder", "scope_reports", "Reports", `["code.run"]`); !errors.Is(err, db.ErrSpaceInvalid) {
		t.Fatalf("a read-only folder asked to run commands: %v", err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
	// The code tools plus document reading through the folder tools.
	if len(tools) != len(codeToolDefinitions())+2 || tools["code.run"] == nil || tools[filesReadTool] == nil {
		t.Fatalf("chat with a project grant has tools %v", tools)
	}
	invocation := agenttools.Invocation{UserID: owner.ID, RunID: record.ID}

	done := make(chan json.RawMessage, 1)
	go func() {
		raw, err := tools["code.run"](ctx, invocation, agent.ToolRequest{ID: "c1", Name: "code.run", Arguments: json.RawMessage(`{"command":"npm run build","cwd":"","timeoutSeconds":60}`)})
		if err != nil {
			t.Error(err)
		}
		done <- raw
	}()
	var jobID, input, operation, capability string
	for deadline := time.Now().Add(10 * time.Second); jobID == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		_ = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT id,input::text,operation,required_capability FROM workflow_device_node_jobs WHERE invocation_id=$1`, record.ID).Scan(&jobID, &input, &operation, &capability)
		})
	}
	if jobID == "" || operation != "code.run" || capability != "code.run" || !strings.Contains(input, "npm run build") || strings.Contains(input, "project") {
		t.Fatalf("queued job = %q %q %q %q", jobID, operation, capability, input)
	}
	var scope string
	_ = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT scope_id FROM workflow_device_node_jobs WHERE id=$1`, jobID).Scan(&scope)
	})
	if scope != "scope_site" {
		t.Fatalf("job scope = %q", scope)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE workflow_device_node_jobs SET state='completed',output='{"exitCode":0,"output":"built in 120ms"}',completed_at=NOW() WHERE id=$1`, jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-done:
		if !strings.Contains(string(raw), "built in 120ms") {
			t.Fatalf("code.run = %s", raw)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("code.run did not return after the device finished")
	}

	// The system prompt explains the project only when its tools are there.
	if guidance := agentCapabilityGuidance([]string{"code.run", "code.preview"}, false); !strings.Contains(guidance, "Projects:") || !strings.Contains(guidance, "code_preview") {
		t.Fatalf("project guidance missing: %s", guidance)
	}
	if strings.Contains(agentCapabilityGuidance([]string{"web.search"}, false), "Projects:") {
		t.Fatal("project guidance without project tools")
	}
}
