package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// The agentic browser tables (replays, notebooks, sub-agents) against real
// Postgres: MISTY_AGENTIC_TEST_DSN must name a disposable database called
// misty_agentic_browser_test. Each test runs in its own schema.
func agenticTestDB(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_AGENTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_agentic_browser_test PostgreSQL")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = admin.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_agentic_browser_test" {
		admin.Close()
		t.Fatal("refusing non-disposable database")
	}
	schema := "agentic_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("postgres", dsn+" search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() {
		conn.Close()
		admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		admin.Close()
	})
	if _, err = conn.Exec(`CREATE TABLE account_cloud_mutations(transaction_id bigint,user_id text,added_bytes bigint,removed_bytes bigint);
 CREATE TABLE users(id text PRIMARY KEY); INSERT INTO users VALUES('owner'),('other');
 CREATE FUNCTION misty_rls_user_id() RETURNS text LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.current_user_id',true),'') $$;
 CREATE FUNCTION misty_rls_is_service() RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('app.rls_mode',true)='service' $$;
 CREATE TABLE ai_invocations(id text PRIMARY KEY, user_id text, state text, error_code text DEFAULT '');
 CREATE TABLE ai_invocation_events(invocation_id text, sequence bigint, event_type text, payload jsonb);
 CREATE TABLE misty_ask_conversations(id text PRIMARY KEY, user_id text, title text, deleted_at timestamptz);
 CREATE TABLE workflow_device_node_jobs(id text PRIMARY KEY, user_id text, invocation_id text, operation text, state text, input jsonb, output jsonb, created_at timestamptz DEFAULT clock_timestamp(), execution_started_at timestamptz,
  leased_device_id text, lease_token_hash text, lease_expires_at timestamptz, delivery_attempts integer NOT NULL DEFAULT 0);`); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"migrations/20271010010000_browser_workflow_replays.sql", "migrations/20271010020000_agent_notebooks_and_delegations.sql"} {
		migration, err := migrationFiles.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(migration), "-- +goose Down")
		if _, err = conn.Exec(up); err != nil {
			t.Fatal(file, err)
		}
	}
	return &Database{Conn: conn}
}

func TestBrowserWorkflowReplaysAreOwnedAndReplaced(t *testing.T) {
	database := agenticTestDB(t)
	ctx := context.Background()
	if replay, err := database.BrowserWorkflowReplayFor(ctx, "owner", "method-v1"); err != nil || replay != nil {
		t.Fatal("expected no replay", replay, err)
	}
	save := func(source string) {
		if err := database.SaveBrowserWorkflowReplay(ctx, "owner", BrowserWorkflowReplay{MethodVersionID: "method-v1", StartURL: "https://a.example", Calls: json.RawMessage(`[{"tool":"browser.navigate","args":{"url":"https://a.example"}}]`), SourceInvocationID: source}); err != nil {
			t.Fatal(err)
		}
	}
	save("invocation_1")
	save("invocation_2")
	replay, err := database.BrowserWorkflowReplayFor(ctx, "owner", "method-v1")
	if err != nil || replay == nil || replay.SourceInvocationID != "invocation_2" || replay.StartURL != "https://a.example" {
		t.Fatal("the newest successful run should replace the replay", replay, err)
	}
	if other, _ := database.BrowserWorkflowReplayFor(ctx, "other", "method-v1"); other != nil {
		t.Fatal("a replay leaked across accounts")
	}
	if err := database.SaveBrowserWorkflowReplay(ctx, "owner", BrowserWorkflowReplay{MethodVersionID: "m", Calls: json.RawMessage(`not json`)}); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatal("invalid calls were saved", err)
	}
	if err := database.DeleteBrowserWorkflowReplay(ctx, "owner", "method-v1"); err != nil {
		t.Fatal(err)
	}
	if replay, _ := database.BrowserWorkflowReplayFor(ctx, "owner", "method-v1"); replay != nil {
		t.Fatal("deleted replay still returned")
	}
}

func TestCompletedBrowserJobsAndToolNamesComeBackInOrder(t *testing.T) {
	database := agenticTestDB(t)
	ctx := context.Background()
	if _, err := database.Conn.Exec(`INSERT INTO ai_invocations VALUES('inv','owner','completed','');
 INSERT INTO workflow_device_node_jobs(id,user_id,invocation_id,operation,state,input,output) VALUES
  ('j1','owner','inv','browser.snapshot','completed','{}','{"url":"https://x"}'),
  ('j2','owner','inv','browser.steps','completed','{"steps":[]}','{"completed":0}'),
  ('j3','owner','inv','browser.steps','failed','{}',NULL),
  ('j4','owner','inv','folder.read','completed','{}','{}'),
  ('j5','other','inv','browser.navigate','completed','{}','{}');
 INSERT INTO ai_invocation_events VALUES('inv',1,'tool.started','{"toolName":"browser.snapshot"}'),('inv',2,'tool.completed','{"toolName":"browser.snapshot"}'),('inv',3,'tool.started','{"toolName":"browser.steps"}');`); err != nil {
		t.Fatal(err)
	}
	jobs, err := database.CompletedBrowserJobs(ctx, "owner", "inv")
	if err != nil || len(jobs) != 2 || jobs[0].Operation != "browser.snapshot" || jobs[1].Operation != "browser.steps" {
		t.Fatal("expected the two completed browser jobs in order", jobs, err)
	}
	names, err := database.InvocationToolNames(ctx, "owner", "inv")
	if err != nil || strings.Join(names, ",") != "browser.snapshot,browser.steps" {
		t.Fatal(names, err)
	}
}

func TestOnlyAnUnstartedClaimIsReleasedByItsOwnToken(t *testing.T) {
	database := agenticTestDB(t)
	hash := TestingHashToken("token")
	if _, err := database.Conn.Exec(`INSERT INTO workflow_device_node_jobs(id,user_id,invocation_id,operation,state,leased_device_id,lease_token_hash,lease_expires_at,delivery_attempts,execution_started_at) VALUES
  ('held','owner','inv','browser.snapshot','leased','device_a',$1,NOW(),1,NULL),
  ('begun','owner','inv','browser.steps','executing','device_a',$1,NOW(),1,NOW())`, hash); err != nil {
		t.Fatal(err)
	}
	state := func(id string) (current string, attempts int, device sql.NullString) {
		if err := database.Conn.QueryRow(`SELECT state,delivery_attempts,leased_device_id FROM workflow_device_node_jobs WHERE id=$1`, id).Scan(&current, &attempts, &device); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, attempt := range []struct{ user, device, job, token string }{
		{"owner", "device_a", "held", "wrong"},
		{"owner", "device_b", "held", "token"},
		{"other", "device_a", "held", "token"},
		{"owner", "device_a", "begun", "token"},
	} {
		if err := database.ReleaseUnstartedWorkflowDeviceNodeJob(attempt.user, attempt.device, attempt.job, attempt.token); err != nil {
			t.Fatal(err)
		}
	}
	if current, _, _ := state("held"); current != "leased" {
		t.Fatal("a claim was released without its own device and token")
	}
	if current, _, _ := state("begun"); current != "executing" {
		t.Fatal("a job that began must never go back to the queue")
	}
	if err := database.ReleaseUnstartedWorkflowDeviceNodeJob("owner", "device_a", "held", "token"); err != nil {
		t.Fatal(err)
	}
	if current, attempts, device := state("held"); current != "queued" || attempts != 0 || device.Valid {
		t.Fatal("the released job should be queued again without a spent delivery", current, attempts, device)
	}
}

func TestNotebookSectionsAppendReplaceAndStayWithinLimits(t *testing.T) {
	database := agenticTestDB(t)
	ctx := context.Background()
	empty, err := database.AgentNotebook(ctx, "owner", "conversation:c1")
	if err != nil || empty.Plan != "" || empty.Findings != "" {
		t.Fatal(empty, err)
	}
	if _, err := database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "plan", "- [ ] research", false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "findings", "Fact one (https://a)", true); err != nil {
		t.Fatal(err)
	}
	notebook, err := database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "findings", "Fact two (https://b)", true)
	if err != nil || notebook.Findings != "Fact one (https://a)\nFact two (https://b)" || notebook.Plan != "- [ ] research" {
		t.Fatal("append lost a finding", notebook, err)
	}
	if notebook, _ = database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "plan", "- [x] research", false); notebook.Plan != "- [x] research" {
		t.Fatal("replace did not replace", notebook)
	}
	if _, err := database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "plan", strings.Repeat("x", MaxNotebookPlan+1), false); !errors.Is(err, ErrNotebookFull) {
		t.Fatal("an oversized plan was saved", err)
	}
	if _, err := database.WriteAgentNotebook(ctx, "owner", "conversation:c1", "secrets", "x", false); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatal("an unknown section was accepted", err)
	}
	if other, _ := database.AgentNotebook(ctx, "other", "conversation:c1"); other.Findings != "" {
		t.Fatal("a notebook leaked across accounts")
	}
	if err := database.SetAgentNotebookNote(ctx, "owner", "conversation:c1", "note_1"); err != nil {
		t.Fatal(err)
	}
	if notebook, _ := database.AgentNotebook(ctx, "owner", "conversation:c1"); notebook.NoteID != "note_1" {
		t.Fatal("note link not saved", notebook)
	}
}

func TestDelegationChildrenReportStateAndAnswers(t *testing.T) {
	database := agenticTestDB(t)
	ctx := context.Background()
	if _, err := database.Conn.Exec(`INSERT INTO ai_invocations VALUES('child-a','owner','completed',''),('child-b','owner','running',''),('child-c','owner','failed','task_blocked');
 INSERT INTO ai_invocation_events VALUES('child-a',1,'assistant.message','{"text":"draft"}'),('child-a',2,'assistant.message','{"text":"{\"price\": 12}"}');`); err != nil {
		t.Fatal(err)
	}
	for index, child := range []string{"child-a", "child-b", "child-c"} {
		if err := database.InsertDelegationChild(ctx, "owner", "parent", DelegationChild{CallID: "call-1", TaskIndex: index, Goal: "goal " + child, ChildInvocationID: child, OutputSchema: json.RawMessage(`{"type":"object"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	// A retried insert is ignored.
	if err := database.InsertDelegationChild(ctx, "owner", "parent", DelegationChild{CallID: "call-1", TaskIndex: 0, Goal: "again", ChildInvocationID: "child-z"}); err != nil {
		t.Fatal(err)
	}
	children, err := database.DelegationChildren(ctx, "owner", "parent", "call-1")
	if err != nil || len(children) != 3 {
		t.Fatal(children, err)
	}
	if children[0].State != "completed" || children[0].Answer != `{"price": 12}` || string(children[0].OutputSchema) != `{"type": "object"}` {
		t.Fatal("the latest answer and schema should come back", children[0])
	}
	if children[1].State != "running" || children[2].State != "failed" || children[2].Error != "task_blocked" {
		t.Fatal(children)
	}
	if parent, _ := database.DelegationParent(ctx, "owner", "child-b"); parent != "parent" {
		t.Fatal("child did not resolve to its parent", parent)
	}
	if parent, _ := database.DelegationParent(ctx, "other", "child-b"); parent != "" {
		t.Fatal("another account resolved a child", parent)
	}
	if mine, _ := database.DelegationChildren(ctx, "other", "parent", ""); len(mine) != 0 {
		t.Fatal("children leaked across accounts")
	}
}
