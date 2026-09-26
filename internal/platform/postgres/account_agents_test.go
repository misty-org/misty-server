package db

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAccountAgentMigrationPreservesHistory(t *testing.T) {
	database, _ := syncTestDatabase(t)
	_, err := database.Conn.Exec(`CREATE FUNCTION misty_rls_is_service() RETURNS boolean LANGUAGE sql AS 'SELECT true';
 CREATE FUNCTION misty_rls_user_id() RETURNS text LANGUAGE sql AS 'SELECT current_setting(''app.user_id'',true)';
 CREATE TABLE spaces(id text primary key);
 INSERT INTO spaces VALUES('former');
 CREATE FUNCTION misty_is_shared_space_run_visible(text,text,text) RETURNS boolean LANGUAGE sql AS 'SELECT false';`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{"misty_agent_execution_leases", "misty_ask_conversations", "misty_memories", "ai_invocations", "ai_invocation_contexts", "space_runs", "agent_run_jobs", "agent_run_contexts"}
	for _, table := range tables {
		_, err = database.Conn.Exec(`CREATE TABLE ` + table + `(id text primary key,space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  agent_id text,owner_user_id text,requesting_member_id text,source_type text,source_conversation_id text);
  INSERT INTO ` + table + `(id,space_id) VALUES('history','former');`)
		if err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../../../test/fixtures/schema-history/20270215000000_account_agent_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(migration), "-- +goose Down")[0]
	tx, err := database.Conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(up); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`DELETE FROM spaces WHERE id='former'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		var retained bool
		if err = tx.QueryRow(`SELECT space_id IS NULL FROM ` + table + ` WHERE id='history'`).Scan(&retained); err != nil || !retained {
			t.Fatalf("%s lost history: %v", table, err)
		}
		if _, err = tx.Exec(`INSERT INTO ` + table + `(id) VALUES('account-only')`); err != nil {
			t.Fatalf("%s requires Space: %v", table, err)
		}
	}
}

func TestAccountAgentMemoryIgnoresLegacyDestination(t *testing.T) {
	database, _ := syncTestDatabase(t)
	_, err := database.Conn.Exec(`CREATE TABLE misty_memories(id text,user_id text,space_id text,agent_id text,kind text,content text,reason text,source_conversation_id text,source_invocation_id text,last_used_at timestamptz,created_at timestamptz,updated_at timestamptz,forgotten_at timestamptz);
 INSERT INTO misty_memories VALUES
 ('personal','owner',NULL,'one','fact','personal','',NULL,NULL,NULL,NOW(),NOW(),NULL),
 ('historical','owner','former','one','fact','history','',NULL,NULL,NULL,NOW(),NOW(),NULL),
 ('other-agent','owner',NULL,'two','fact','other agent','',NULL,NULL,NULL,NOW(),NOW(),NULL),
 ('other-user','other',NULL,'one','fact','other user','',NULL,NULL,NULL,NOW(),NOW(),NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{"", "former", "unrelated"} {
		memories, err := database.MistyMemories(context.Background(), "owner", destination, 100, "one")
		if err != nil || len(memories) != 2 {
			t.Fatalf("destination %q: %v %v", destination, memories, err)
		}
		for _, memory := range memories {
			if memory.SpaceID != "" || (memory.ID != "personal" && memory.ID != "historical") {
				t.Fatalf("invalid memory: %+v", memory)
			}
		}
	}
	if err := database.UpdateAgentMemory(context.Background(), "other", "one", "", "personal", "overwrite"); err == nil {
		t.Fatal("another account changed memory")
	}
}

func TestAccountAgentLeaseIgnoresSpaceAndFencesIdentity(t *testing.T) {
	database, _ := syncTestDatabase(t)
	_, err := database.Conn.Exec(`
 CREATE TABLE misty_ask_identities(id text primary key,owner_user_id text,name text DEFAULT 'Agent',role text DEFAULT '',description text DEFAULT '',icon text DEFAULT '',avatar jsonb DEFAULT '{}',instructions text DEFAULT '',model_mode text DEFAULT 'automatic',model_id text DEFAULT '',reasoning_effort text DEFAULT '',default_run_mode text DEFAULT 'auto',voice_id text DEFAULT '',enabled boolean DEFAULT true,system_managed boolean DEFAULT false,version bigint DEFAULT 1,created_at timestamptz DEFAULT NOW(),updated_at timestamptz DEFAULT NOW(),deleted_at timestamptz);
 INSERT INTO misty_ask_identities(id,owner_user_id) VALUES('one','owner'),('foreign','other');
 CREATE TABLE misty_agent_execution_leases(owner_user_id text,agent_id text,space_id text,task_id text UNIQUE,window_label text,expires_at timestamptz,PRIMARY KEY(owner_user_id,agent_id));`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	input := AgentExecutionLease{AgentID: "one", TaskID: "task", WindowLabel: "worker", SpaceID: "obsolete"}
	if err = database.AcquireAgentExecution(ctx, "owner", input); err != nil {
		t.Fatal(err)
	}
	input.Renew = true
	input.SpaceID = "different"
	if err = database.AcquireAgentExecution(ctx, "owner", input); err != nil {
		t.Fatal(err)
	}
	input.WindowLabel = "different"
	if err = database.AcquireAgentExecution(ctx, "owner", input); err == nil {
		t.Fatal("another window renewed lease")
	}
	input.WindowLabel = "worker"
	if err = database.AcquireAgentExecution(ctx, "other", input); err == nil {
		t.Fatal("another account renewed lease")
	}
	record := &AIInvocationRecord{UserID: "owner", SpaceID: "historical", RequestPayload: []byte(`{"agent_id":"one","execution_mode":"agent","task_id":"task","window_label":"worker"}`)}
	if err = database.ValidateNativeAgentExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = database.ReleaseAgentExecution(ctx, "owner", "task"); err != nil {
		t.Fatal(err)
	}
	if err = database.ValidateNativeAgentExecution(ctx, record); err == nil {
		t.Fatal("released task retained authority")
	}
}

// Exercise the canonical SQL lifecycle with no Spaces table. The fixture keeps
// the legacy run response columns so new account work uses the existing store.
func TestAccountAgentBackgroundRunWithoutSpaces(t *testing.T) {
	database, _ := syncTestDatabase(t)
	columns := regexp.MustCompile(`COALESCE\((\w+),''\)`).ReplaceAllString(spaceRunColumns, "$1")
	definitions := []string{}
	for _, column := range strings.Split(columns, ",") {
		column = strings.TrimSpace(column)
		kind := "text DEFAULT ''"
		switch column {
		case "id":
			kind = "text PRIMARY KEY"
		case "space_id":
			kind = "text"
		case "progress", "attempt", "delegation_depth":
			kind = "integer DEFAULT 0"
		case "created_at", "updated_at":
			kind = "timestamptz DEFAULT NOW()"
		case "completed_at", "canceled_at", "next_retry_at", "runtime_heartbeat_at", "device_wait_expires_at":
			kind = "timestamptz"
		case "input", "result", "outputs", "artifacts", "action_envelope", "agent_version_snapshot", "context_bindings":
			kind = "jsonb DEFAULT '{}'"
		}
		definitions = append(definitions, column+" "+kind)
	}
	if _, err := database.Conn.Exec("CREATE TABLE space_runs(" + strings.Join(definitions, ",") + ")"); err != nil {
		t.Fatal(err)
	}
	_, err := database.Conn.Exec(`
 CREATE TABLE misty_ask_identities(id text,owner_user_id text,name text,instructions text,model_id text,reasoning_effort text,default_run_mode text,system_managed boolean,version bigint,enabled boolean,deleted_at timestamptz);
 CREATE TABLE misty_ask_identity_versions(id text,agent_id text,version bigint);
 INSERT INTO misty_ask_identities VALUES('agent','owner','Agent','','model','medium','auto',true,1,true,NULL);
 INSERT INTO misty_ask_identity_versions VALUES('version','agent',1);
 CREATE TABLE ai_invocations(id text,user_id text,request_payload jsonb);
 INSERT INTO ai_invocations VALUES('invocation_parent','owner','{}');
 CREATE TABLE agent_sdk_capability_bindings(run_id text,user_id text,target_id text,target_revision integer,capability text,capability_version integer,provider_id text,provider_version integer,adapter_version text,space_run_id text,ai_invocation_id text);
 CREATE TABLE agent_run_jobs(run_id text,space_id text,task_id text,agent_id text,trigger_kind text);`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	input := CreatorAgentRunInput{Instruction: "Explain this page", AIInvocationID: "invocation_parent"}
	run, err := database.CreateCreatorAgentRun(ctx, "owner", "obsolete", "agent", input)
	if err != nil {
		t.Fatal(err)
	}
	if run.SpaceID != "" || run.OwnerUserID != "owner" || run.State != "queued" {
		t.Fatalf("incorrect run: %+v", run)
	}
	if _, err = database.CreateCreatorAgentRun(ctx, "other", "", "agent", input); err == nil {
		t.Fatal("another account launched the Agent")
	}
	completed, err := database.FinishSpaceRun(ctx, run.ID, "completed", []byte(`{"message":"Done"}`), "")
	if err != nil || completed.State != "completed" {
		t.Fatalf("account run did not finish: %+v %v", completed, err)
	}
}
