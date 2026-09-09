package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Reconstruct the pre-migration names inside one transaction. The test never
// rolls back an applied migration or touches a developer database.
func TestGlobalAskMigrationPreservesAskAndSelectivelyPurgesLegacy(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Ask migration", "ask-migration@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Ask migration")
	if err != nil {
		t.Fatal(err)
	}
	ask, err := database.EnsureAskIdentity(ctx, owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, ask.ID, CreatorAgentRunInput{Instruction: "Preserve this work"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := database.CreateSpaceTask(ctx, owner.ID, SpaceTask{SpaceID: space.ID, Title: "Preserved task", Status: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../internal/platform/postgres/migrations/20270130000000_global_ask_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	migration := strings.Split(strings.Split(string(raw), "-- +goose Up")[1], "-- +goose Down")[0]
	globalConversation := "conversation_" + uuid.NewString()
	managedConversation := "conversation_" + uuid.NewString()
	legacyConversation := "conversation_" + uuid.NewString()
	connectionID := "connection_" + uuid.NewString()
	err = database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		exec := func(query string, args ...any) error { _, err := tx.ExecContext(ctx, query, args...); return err }
		if err := exec(`CREATE TABLE space_agents(id text PRIMARY KEY, published_agent_version_id text);
CREATE TABLE space_agent_conversation_events(id text);
CREATE TABLE space_agent_conversations(id text);
CREATE TABLE space_agent_instance_workflows(id text);
CREATE TABLE space_agent_memory_events(id text);
CREATE TABLE space_workflow_event_claims(id text);
CREATE TABLE space_agent_message_triggers(id text);
CREATE TABLE space_agent_version_workflows(id text);
CREATE TABLE space_agent_instances(id text);
CREATE TABLE space_agent_versions(id text);
CREATE TABLE space_action_suggestion_dismissals(id text);
CREATE TABLE space_action_suggestion_items(id text);
CREATE TABLE space_action_suggestion_jobs(id text);
CREATE TABLE space_action_suggestion_batches(id text);
CREATE TABLE space_action_suggestion_settings(id text);
CREATE TABLE space_conversation_follow_up_recipients(id text);
CREATE TABLE space_conversation_suggestion_vetoes(id text);
CREATE TABLE space_conversation_follow_ups(id text, agent_id text);
ALTER TABLE ai_user_settings ADD COLUMN active_companion_agent_id text;
ALTER TABLE ai_surface_preferences ADD COLUMN pinned_agent_id text;
ALTER TABLE misty_ask_identities DROP CONSTRAINT misty_ask_identity_only;
ALTER TABLE misty_ask_identities ADD COLUMN source_space_agent_id text;
ALTER TABLE misty_ask_conversations ADD COLUMN personal_agent_id text;
ALTER TABLE misty_ask_identities RENAME TO personal_agents;
ALTER TABLE misty_ask_identity_versions RENAME TO personal_agent_versions;
ALTER TABLE misty_ask_mcp_tools RENAME TO personal_agent_mcp_tools;
ALTER TABLE misty_ask_conversations RENAME TO agent_conversations;
ALTER TABLE misty_ask_conversation_events RENAME TO agent_conversation_events;`); err != nil {
			return err
		}
		if err := exec(`INSERT INTO personal_agents(id,owner_user_id,name,model_id,system_managed) VALUES('retired-test-agent',$1,'Retired','google/gemini-2.5-flash-lite',false)`, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_conversations(id,user_id,personal_agent_id,conversation_kind,space_id)
VALUES($1,$4,NULL,'companion_task',$5),($2,$4,$6,'companion_task',$5),($3,$4,'retired-test-agent','companion_task',$5)`, globalConversation, managedConversation, legacyConversation, owner.ID, space.ID, ask.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_conversation_events(conversation_id,user_id,event_type,data)
SELECT id,user_id,'user_message','{"text":"history"}' FROM agent_conversations WHERE user_id=$1`, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO space_runs SELECT (jsonb_populate_record(NULL::space_runs,to_jsonb(r)||'{"id":"retired-test-run","agent_id":"retired-test-agent","resource_id":"retired-test-agent","agent_version_snapshot":{},"input":{}}')).* FROM space_runs r WHERE r.id=$1`, run.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_run_jobs(run_id,space_id,agent_id) VALUES('retired-test-run',$1,'retired-test-agent')`, space.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_run_tool_approvals(id,run_id,owner_user_id,tool_call_id,tool_name,impact,arguments_hash,signed_call,hook_token)
VALUES('preserved-approval',$1,$2,'call','mail.send','consequential','hash','signed','hook'),('retired-approval','retired-test-run',$2,'call','mail.send','consequential','hash','signed','old-hook')`, run.ID, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO ai_invocations(id,user_id,conversation_id,surface_id,mode,trigger_kind,state,idempotency_key,agent_run_id)
VALUES('retired-invocation',$1,$2,'misty','drawer','message','completed','legacy','retired-test-run'),('preserved-invocation',$1,$3,'misty','drawer','message','queued','ask',$4)`, owner.ID, legacyConversation, globalConversation, run.ID); err != nil {
			return err
		}
		if err := exec(`UPDATE space_tasks SET created_by_agent_id='retired-test-agent',assignee_agent_id='retired-test-agent',source_run_id='retired-test-run' WHERE id=$1`, task.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO connected_accounts(id,user_id,provider,account_id,credential_ciphertext,credential_nonce) VALUES($1,$2,'google','pilot','\x01','\x02')`, connectionID, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO space_conversations(id,space_id,title,created_by_user_id,kind,direct_user_id,direct_agent_id)
VALUES('retired-private-conversation',$1,'Private agent work',$2,'direct',$2,'retired-test-agent');`, space.ID, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO space_conversation_members(conversation_id,actor_kind,user_id,agent_id)
VALUES('retired-private-conversation','person',$1,NULL),('retired-private-conversation','agent',NULL,'retired-test-agent')`, owner.ID); err != nil {
			return err
		}
		if err := exec(`UPDATE space_tasks SET audience_kind='conversation',audience_conversation_id='retired-private-conversation',audience_creator_user_id=$2 WHERE id=$1`, task.ID, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_runtime_deliveries(id,user_id,run_id,operation)
VALUES('retired-delivery',$1,'retired-invocation','invocation.start'),('preserved-delivery',$1,'preserved-invocation','invocation.start')`, owner.ID); err != nil {
			return err
		}
		if err := exec(`INSERT INTO agent_toolbox_action_journal(idempotency_key,user_id,agent_id,run_id,tool_name,audit_event,risk,source,state)
VALUES('retired-effect',$1,'retired-test-agent','retired-test-run','mail.send','send','write','test','completed'),('preserved-effect',$1,$2,$3,'mail.send','send','write','test','completed')`, owner.ID, ask.ID, run.ID); err != nil {
			return err
		}
		if err := exec(`UPDATE personal_agents SET avatar='{"kind":"upload","asset_id":"avatar_12345678-1234-1234-1234-123456789abc","version":1}' WHERE id='retired-test-agent'`); err != nil {
			return err
		}
		if err := exec(migration); err != nil {
			return err
		}
		checks := []struct {
			query string
			want  int
		}{
			{`SELECT COUNT(*) FROM space_conversations WHERE id='retired-private-conversation' AND kind='standard' AND NOT visible_to_space AND direct_agent_id IS NULL`, 1},
			{`SELECT COUNT(*) FROM space_conversation_members WHERE conversation_id='retired-private-conversation' AND actor_kind='person'`, 1},
			{`SELECT COUNT(*) FROM space_conversation_members WHERE actor_kind='agent'`, 0},
			{`SELECT COUNT(*) FROM space_tasks WHERE id='` + task.ID + `' AND audience_kind='conversation' AND audience_conversation_id='retired-private-conversation'`, 1},
			{`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('space_agents','space_agent_instances','space_agent_message_triggers','space_action_suggestion_jobs')`, 0},
			{`SELECT COUNT(*) FROM agent_runtime_deliveries WHERE id='retired-delivery'`, 0},
			{`SELECT COUNT(*) FROM agent_runtime_deliveries WHERE id='preserved-delivery'`, 1},
			{`SELECT COUNT(*) FROM agent_toolbox_action_journal WHERE idempotency_key='retired-effect'`, 0},
			{`SELECT COUNT(*) FROM agent_toolbox_action_journal WHERE idempotency_key='preserved-effect'`, 1},
			{`SELECT COUNT(*) FROM object_deletion_jobs WHERE object_key='avatars/avatar_12345678-1234-1234-1234-123456789abc'`, 1},
			{`SELECT COUNT(*) FROM misty_ask_identities WHERE NOT system_managed`, 0},
			{`SELECT COUNT(*) FROM misty_ask_conversations WHERE user_id='` + owner.ID + `'`, 2},
			{`SELECT COUNT(*) FROM misty_ask_conversation_events`, 2},
			{`SELECT COUNT(*) FROM agent_run_jobs WHERE run_id='retired-test-run'`, 0},
			{`SELECT COUNT(*) FROM agent_run_tool_approvals WHERE id='retired-approval'`, 0},
			{`SELECT COUNT(*) FROM agent_run_tool_approvals WHERE id='preserved-approval'`, 1},
			{`SELECT COUNT(*) FROM ai_invocations WHERE id='retired-invocation'`, 0},
			{`SELECT COUNT(*) FROM ai_invocations WHERE id='preserved-invocation'`, 1},
			{`SELECT COUNT(*) FROM connected_accounts WHERE id='` + connectionID + `'`, 1},
			{`SELECT COUNT(*) FROM space_tasks WHERE id='` + task.ID + `' AND assignee_agent_id IS NULL AND source_run_id IS NULL AND title='Preserved task'`, 1},
		}
		for _, check := range checks {
			var got int
			if err := tx.QueryRowContext(ctx, check.query).Scan(&got); err != nil {
				return err
			}
			if got != check.want {
				t.Errorf("%s: got %d, want %d", check.query, got, check.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := database.EnsureAskIdentity(ctx, owner.ID, ask.ModelID)
	if err != nil || preserved.ID != ask.ID {
		t.Fatalf("Ask identity changed: %#v %v", preserved, err)
	}
	if _, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, ask.ID, CreatorAgentRunInput{Instruction: "Continue after migration"}); err != nil {
		t.Fatalf("Ask operation after migration: %v", err)
	}
	if _, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, "retired-test-agent", CreatorAgentRunInput{Instruction: "Must not dispatch"}); err == nil {
		t.Fatal("retired identity admitted")
	}
}
