package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Exercise the production claim/begin/renew path against PostgreSQL, including
// revocation of a download's source after its destination job was admitted.
func TestNativeDownloadUploadAuthority(t *testing.T) {
	for _, scenario := range []string{"valid", "source_expired", "source_window", "source_device", "source_capability", "source_unattached"} {
		t.Run(scenario, func(t *testing.T) {
			database := openTestDatabase(t)
			ctx := t.Context()
			owner, err := database.CreateUser("Catalog test", "catalog-test@example.com", "password123")
			if err != nil {
				t.Fatal(err)
			}
			space := createTestSpace(t, database, ctx, owner.ID, "Catalog")
			agent, err := database.SavePersonalAgent(ctx, owner.ID, "", AgentProfileInput{Name: "Catalog", Role: "Prepare catalog", ModelMode: "automatic", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			lease := AgentExecutionLease{AgentID: agent.ID, SpaceID: space.ID, TaskID: "catalog-task", WindowLabel: "main"}
			if err := database.AcquireAgentExecution(ctx, owner.ID, lease); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]string{"agent_id": agent.ID, "execution_mode": "agent", "task_id": lease.TaskID, "window_label": lease.WindowLabel})
			invocation, _, err := database.CreateAIInvocationRecord(ctx, AIInvocationRecord{
				ID: "invocation_catalog_files", UserID: owner.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: "catalog-files", RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			key, _, _ := ed25519.GenerateKey(rand.Reader)
			device, err := database.RegisterTrustedDevice(owner.ID, "Catalog Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
			if err != nil {
				t.Fatal(err)
			}
			var source *AIInvocationContext
			for _, target := range []struct{ scope, app, capability string }{{"image-source", "browser", "browser.downloads.list"}, {"catalog-destination", "journal", "browser.upload"}} {
				capabilities, _ := json.Marshal([]string{target.capability})
				metadata, _ := json.Marshal(map[string]string{"app_id": target.app, "window_label": "main"})
				attached, err := database.AttachAIInvocationContext(ctx, owner.ID, invocation.ID, space.ID, device.ID, "browser_tab", target.scope, target.scope, capabilities, metadata)
				if err != nil {
					t.Fatal(err)
				}
				if target.app == "browser" {
					source = attached
				}
			}
			if _, err := database.ActivateAIInvocationRuntime(ctx, invocation.ID, "vercel-workflow", "runtime-catalog"); err != nil {
				t.Fatal(err)
			}
			job, err := database.QueueAIInvocationDeviceNodeJob(ctx, owner.ID, invocation.ID, "upload", 1, "catalog-destination", "browser.upload", "browser.upload",
				json.RawMessage(`{"downloadId":"download-one","sourceScopeId":"image-source"}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`))
			if err != nil {
				t.Fatal(err)
			}
			claimed, token, err := database.ClaimWorkflowDeviceNodeJob(owner.ID, device.ID, time.Minute, 2)
			if err != nil || claimed == nil || claimed.ID != job.ID {
				t.Fatalf("claim: %v", err)
			}
			if scenario == "source_revoked_after_begin" {
				if _, err := database.BeginWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "source_expired":
				_, err = database.Conn.Exec(`UPDATE ai_invocation_contexts SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, source.ID)
			case "source_window":
				_, err = database.Conn.Exec(`UPDATE ai_invocation_contexts SET metadata=jsonb_set(metadata,'{window_label}','"other-window"') WHERE id=$1`, source.ID)
			case "source_device":
				otherKey, _, _ := ed25519.GenerateKey(rand.Reader)
				otherDevice, deviceErr := database.RegisterTrustedDevice(owner.ID, "Other Mac", base64.RawURLEncoding.EncodeToString(otherKey), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
				if deviceErr != nil {
					t.Fatal(deviceErr)
				}
				_, err = database.Conn.Exec(`UPDATE ai_invocation_contexts SET device_id=$2 WHERE id=$1`, source.ID, otherDevice.ID)
			case "source_capability":
				_, err = database.Conn.Exec(`UPDATE ai_invocation_contexts SET capabilities='["browser.inspect"]' WHERE id=$1`, source.ID)
			case "source_unattached":
				_, err = database.Conn.Exec(`UPDATE workflow_device_node_jobs SET input=jsonb_set(input,'{sourceScopeId}','"unattached-source"') WHERE id=$1`, job.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "source_revoked_after_begin" {
				if _, err = database.RenewWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token); err == nil {
					t.Fatal("renewal retained revoked source access")
				}
				return
			}
			_, err = database.BeginWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("began upload after source or destination authority changed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = database.RenewWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token); err != nil {
				t.Fatal(err)
			}
			output := json.RawMessage(`{"inputSelected":true,"websiteUploadVerified":false}`)
			for range 2 {
				if _, err = database.FinishWorkflowDeviceNodeJob(owner.ID, device.ID, job.ID, token, "completed", output, ""); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
