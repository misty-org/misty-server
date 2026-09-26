package api

import (
	"context"
	"testing"
)

func TestNativeAgentWorkspaceManifestWithoutSpace(t *testing.T) {
	database, _ := browserSocketTestDatabase(t)
	_, err := database.Conn.Exec(`
        CREATE TABLE misty_ask_identities(id text primary key, owner_user_id text, enabled boolean, deleted_at timestamptz);
        INSERT INTO misty_ask_identities VALUES('agent','owner',true,NULL);
        CREATE TABLE misty_agent_execution_leases(owner_user_id text,agent_id text,space_id text,task_id text,window_label text,expires_at timestamptz);
        INSERT INTO misty_agent_execution_leases VALUES('owner','agent',NULL,'task','main',NOW()+INTERVAL '1 hour');
        CREATE TABLE ai_invocations(id text,user_id text,space_id text,conversation_id text,surface_id text,mode text,trigger_kind text,state text,idempotency_key text,request_payload jsonb,runtime_kind text,runtime_run_id text,agent_run_id text,runtime_heartbeat_at timestamptz,expires_at timestamptz,created_at timestamptz,updated_at timestamptz,model_turn_limit integer);
        INSERT INTO ai_invocations VALUES('invocation_personal','owner',NULL,NULL,'global','drawer','message','running','personal','{"agent_id":"agent","execution_mode":"agent","task_id":"task","window_label":"main"}','agent','','',NULL,NOW()+INTERVAL '1 hour',NOW(),NOW(),10);
        CREATE TABLE ai_invocation_contexts(id text,invocation_id text,user_id text,space_id text,device_id text,kind text,opaque_ref text,display_name text,capabilities jsonb,metadata jsonb,state text,expires_at timestamptz,created_at timestamptz,updated_at timestamptz);
        INSERT INTO ai_invocation_contexts VALUES('context','invocation_personal','owner','','device','browser_tab','scope','Browser','["browser.inspect","browser.navigate"]','{"app_id":"browser","window_label":"main"}','attached',NOW()+INTERVAL '1 hour',NOW(),NOW());
    `)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := spaceConversationToolActor{userID: "owner", agentID: "agent", runID: "invocation_personal"}
	_, _, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, actor, "Open the website", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"browser.inspect", "browser.navigate", "agents.list", "memory.list", "spaces.list", "spaces.tools", "spaces.execute"} {
		if !agentManifestHasTool(manifest, name) {
			t.Errorf("personal manifest missing %s", name)
		}
	}
	for _, name := range []string{"tasks.create", "context.get", "browser.upload", "browser.workspace.interact"} {
		if agentManifestHasTool(manifest, name) {
			t.Errorf("retired or ungranted tool admitted: %s", name)
		}
	}
	if _, err = database.Conn.Exec(`UPDATE misty_agent_execution_leases SET expires_at=NOW()-INTERVAL '1 minute'`); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = resolveAIInvocationSpaceToolbox(ctx, database, actor, "Open the website", "", "")
	if err == nil {
		t.Fatal("expired execution retained tool authority")
	}
}
