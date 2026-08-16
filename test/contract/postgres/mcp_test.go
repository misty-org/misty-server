package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/google/uuid"
)

type mcpPostgresFixture struct {
	database      *Database
	owner         *User
	attacker      *User
	agent         *PersonalAgent
	attackerAgent *PersonalAgent
	connection    *MCPRemoteConnection
	attackerConn  *MCPRemoteConnection
	tool          MCPRemoteTool
	attackerTool  MCPRemoteTool
}

func setupMCPPostgres(t *testing.T) mcpPostgresFixture {
	t.Helper()
	ctx := context.Background()
	database := openTestDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("MCP Owner", "mcp_owner_"+suffix, "mcp-owner-"+suffix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := database.CreateUserWithUsername("MCP Attacker", "mcp_attacker_"+suffix, "mcp-attacker-"+suffix+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	createAgent := func(user *User, name string) *PersonalAgent {
		agent, createErr := database.CreatePersonalAgent(ctx, user.ID, PersonalAgent{
			Name: name, ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return agent
	}
	createConnection := func(user *User, name, endpoint string) *MCPRemoteConnection {
		connection, createErr := database.CreateMCPRemoteConnection(ctx, MCPRemoteConnection{
			OwnerUserID: user.ID, Name: name, EndpointURL: endpoint,
			BearerCiphertext: []byte("ciphertext"), BearerNonce: []byte("nonce"), KeyVersion: 1,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		connection, createErr = database.SetMCPConnectionHealth(ctx, user.ID, connection.ID, "active", "", false)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return connection
	}
	createTool := func(user *User, connection *MCPRemoteConnection, stableName, fingerprint string) MCPRemoteTool {
		tools := []MCPRemoteTool{{
			RemoteName: "echo", StableName: stableName, Description: "Echo safe text",
			InputSchema: json.RawMessage(`{"type":"object"}`), SchemaFingerprint: fingerprint,
			SchemaStatus: "valid",
		}}
		_, saveErr := database.SaveMCPDiscovery(ctx, user.ID, MCPDiscoverySnapshot{
			ConnectionID: connection.ID, ProtocolVersion: "2026-07-28",
			CatalogFingerprint: fingerprint, ToolCount: 1, Status: "complete",
		}, tools)
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return tools[0]
	}
	connection := createConnection(owner, "Owner MCP", "https://owner-mcp.example/mcp")
	attackerConn := createConnection(attacker, "Attacker MCP", "https://attacker-mcp.example/mcp")
	return mcpPostgresFixture{
		database: database, owner: owner, attacker: attacker,
		agent: createAgent(owner, "Owner MCP Agent"), attackerAgent: createAgent(attacker, "Attacker MCP Agent"),
		connection: connection, attackerConn: attackerConn,
		tool:         createTool(owner, connection, "mcp.owner.echo", strings.Repeat("a", 64)),
		attackerTool: createTool(attacker, attackerConn, "mcp.attacker.echo", strings.Repeat("b", 64)),
	}
}

func TestMCPBindingsEnforceOwnershipRLSAndCompositeToolProvenance(t *testing.T) {
	fixture := setupMCPPostgres(t)
	ctx := context.Background()
	bindings, err := fixture.database.SetPersonalAgentMCPTools(ctx, fixture.owner.ID, fixture.agent.ID, []MCPAgentToolSelection{{
		ConnectionID: fixture.connection.ID, RemoteName: fixture.tool.RemoteName, Enabled: true,
	}})
	if err != nil || len(bindings) != 1 || !bindings[0].Enabled {
		t.Fatalf("owner bindings=%#v err=%v", bindings, err)
	}
	if _, err := fixture.database.SetPersonalAgentMCPTools(ctx, fixture.attacker.ID, fixture.agent.ID, nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("attacker changed owner agent bindings: %v", err)
	}
	if _, err := fixture.database.SetPersonalAgentMCPTools(ctx, fixture.attacker.ID, fixture.attackerAgent.ID, []MCPAgentToolSelection{{
		ConnectionID: fixture.connection.ID, RemoteName: fixture.tool.RemoteName, Enabled: true,
	}}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("attacker bound owner connection: %v", err)
	}

	err = fixture.database.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO personal_agent_mcp_tools
			(id,owner_user_id,agent_id,connection_id,remote_tool_id,stable_name,enabled)
			VALUES($1,$2,$3,$4,$5,$6,TRUE)`, "bad_mcp_binding_"+uuid.NewString(), fixture.owner.ID,
			fixture.agent.ID, fixture.connection.ID, fixture.attackerTool.ID, "mcp.bad.echo")
		return insertErr
	})
	if err == nil {
		t.Fatal("composite provenance accepted a remote tool from another connection")
	}

	tables := []string{"mcp_remote_connections", "mcp_discovery_snapshots", "mcp_remote_tools", "personal_agent_mcp_tools", "mcp_tool_execution_audit"}
	err = fixture.database.TestingWithRLSContext(ctx, map[string]string{
		"app.rls_mode": "user", "app.current_user_id": fixture.attacker.ID,
	}, func(tx *sql.Tx) error {
		var appRoleExists bool
		if roleErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app')`).Scan(&appRoleExists); roleErr != nil {
			return roleErr
		}
		if appRoleExists {
			if _, roleErr := tx.ExecContext(ctx, `SET LOCAL ROLE misty_app`); roleErr != nil {
				return roleErr
			}
		}
		for _, table := range tables {
			var count int
			query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE owner_user_id=$1", table)
			argument := fixture.owner.ID
			if table == "mcp_discovery_snapshots" || table == "mcp_remote_tools" {
				query = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE connection_id=$1", table)
				argument = fixture.connection.ID
			}
			if queryErr := tx.QueryRowContext(ctx, query, argument).Scan(&count); queryErr != nil {
				return queryErr
			}
			if count != 0 {
				return fmt.Errorf("attacker read %d owner rows from %s", count, table)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	changedTools := []MCPRemoteTool{{
		RemoteName: fixture.tool.RemoteName, StableName: fixture.tool.StableName,
		Description: "Changed schema", InputSchema: json.RawMessage(`{"type":"object"}`),
		SchemaFingerprint: strings.Repeat("c", 64), SchemaStatus: "valid",
	}}
	if _, err := fixture.database.SaveMCPDiscovery(ctx, fixture.owner.ID, MCPDiscoverySnapshot{
		ConnectionID: fixture.connection.ID, ProtocolVersion: "2026-07-28",
		CatalogFingerprint: strings.Repeat("c", 64), ToolCount: 1, Status: "complete",
	}, changedTools); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.PersonalAgentMCPToolForExecution(ctx, fixture.owner.ID, fixture.agent.ID, fixture.tool.StableName); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("schema-changed MCP tool remained executable: %v", err)
	}
}

func TestMCPExecutionAuditIsContentFreeIdempotentAndComposite(t *testing.T) {
	fixture := setupMCPPostgres(t)
	ctx := context.Background()
	audit := MCPExecutionAudit{
		OwnerUserID: fixture.owner.ID, AgentID: fixture.agent.ID,
		ConnectionID: fixture.connection.ID, RemoteToolID: fixture.tool.ID,
		RemoteName: fixture.tool.RemoteName, StableName: fixture.tool.StableName,
		RunID: "run-1", IdempotencyKey: "mcp-action-1", Source: "personal_agent", Approved: true,
		Success: true, DurationMS: 12,
	}
	if err := fixture.database.RecordMCPExecutionAudit(ctx, audit); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.RecordMCPExecutionAudit(ctx, audit); err != nil {
		t.Fatal(err)
	}
	audits, err := fixture.database.MCPExecutionAudits(ctx, fixture.owner.ID, fixture.agent.ID, 20)
	if err != nil || len(audits) != 1 {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
	encoded, err := json.Marshal(audits[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"arguments", "input", "output", "result", "content", "bearer", "idempotency"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("audit JSON exposed %q: %s", forbidden, encoded)
		}
	}

	audit.IdempotencyKey = "mcp-action-mismatched-tool"
	audit.RemoteToolID = fixture.attackerTool.ID
	if err := fixture.database.RecordMCPExecutionAudit(ctx, audit); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("mismatched audit provenance error=%v, want ErrSpaceInvalid", err)
	}

	forbiddenColumns := map[string]bool{"arguments": true, "input": true, "output": true, "result": true, "content": true, "bearer_token": true}
	rows, err := fixture.database.Conn.QueryContext(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema='public' AND table_name='mcp_tool_execution_audit'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		if forbiddenColumns[column] {
			t.Fatalf("execution audit persists content-bearing column %q", column)
		}
	}
}

func TestMCPRedactedActionJournalPreventsDuplicateRemoteWrites(t *testing.T) {
	fixture := setupMCPPostgres(t)
	ctx := context.Background()
	action := AgentToolboxAction{
		IdempotencyKey: "mcp:" + fixture.agent.ID + ":call-1",
		UserID:         fixture.owner.ID, AgentID: fixture.agent.ID, RunID: "run-1",
		ToolName: fixture.tool.StableName, AuditEvent: "mcp.tool.execute",
		Risk: "write", Source: "personal_agent", Request: json.RawMessage(`{"secret":"must-not-persist"}`),
		RedactPayload: true,
	}
	executions := 0
	execute := func() (json.RawMessage, error) {
		executions++
		return json.RawMessage(`{"remote_write_id":"write-1"}`), nil
	}
	first, err := fixture.database.JournalAgentToolboxAction(ctx, action, execute)
	if err != nil || executions != 1 || !strings.Contains(string(first), "write-1") {
		t.Fatalf("first result=%s executions=%d err=%v", first, executions, err)
	}
	replayed, err := fixture.database.JournalAgentToolboxAction(ctx, action, execute)
	if err != nil || executions != 1 || string(replayed) != `{}` {
		t.Fatalf("replay result=%s executions=%d err=%v", replayed, executions, err)
	}
	var request, result string
	err = fixture.database.Conn.QueryRowContext(ctx, `SELECT request::text,result::text
		FROM agent_toolbox_action_journal WHERE idempotency_key=$1`, action.IdempotencyKey).Scan(&request, &result)
	if err != nil || request != `{}` || result != `{}` {
		t.Fatalf("redacted journal request=%q result=%q err=%v", request, result, err)
	}

	action.IdempotencyKey += ":failure"
	failures := 0
	remoteFailure := errors.New("remote write outcome unknown")
	_, err = fixture.database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) {
		failures++
		return nil, remoteFailure
	})
	if !errors.Is(err, remoteFailure) || failures != 1 {
		t.Fatalf("first failure attempts=%d err=%v", failures, err)
	}
	_, err = fixture.database.JournalAgentToolboxAction(ctx, action, func() (json.RawMessage, error) {
		failures++
		return nil, nil
	})
	if !errors.Is(err, ErrAgentToolboxActionTerminal) || failures != 1 {
		t.Fatalf("failed action was retried: attempts=%d err=%v", failures, err)
	}
}
