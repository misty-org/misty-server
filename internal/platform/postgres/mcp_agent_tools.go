package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type MCPAgentToolSelection struct {
	ConnectionID string `json:"connection_id"`
	RemoteName   string `json:"remote_name"`
	Enabled      bool   `json:"enabled"`
}

type MCPAgentToolBinding struct {
	AgentID            string          `json:"agent_id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectionName     string          `json:"connection_name"`
	ConnectionProvider string          `json:"-"`
	RemoteToolID       string          `json:"-"`
	RemoteName         string          `json:"remote_name"`
	StableName         string          `json:"stable_name"`
	Description        string          `json:"description"`
	InputSchema        json.RawMessage `json:"input_schema"`
	SchemaFingerprint  string          `json:"-"`
	SchemaStatus       string          `json:"schema_status"`
	DisabledReason     string          `json:"disabled_reason,omitempty"`
	Enabled            bool            `json:"enabled"`
	EndpointURL        string          `json:"-"`
	BearerCipher       []byte          `json:"-"`
	BearerNonce        []byte          `json:"-"`
	ConnectionUp       bool            `json:"-"`
}

type MCPExecutionAudit struct {
	ID             string    `json:"id"`
	OwnerUserID    string    `json:"-"`
	AgentID        string    `json:"agent_id"`
	ConnectionID   string    `json:"connection_id"`
	RemoteToolID   string    `json:"-"`
	RemoteName     string    `json:"remote_name"`
	StableName     string    `json:"stable_name"`
	RunID          string    `json:"-"`
	IdempotencyKey string    `json:"-"`
	Source         string    `json:"source"`
	Approved       bool      `json:"approved"`
	Success        bool      `json:"success"`
	ErrorCode      string    `json:"error_code,omitempty"`
	DurationMS     int       `json:"duration_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

const mcpAgentToolColumns = `a.id,c.id,c.name,c.provider,t.id,t.remote_name,t.stable_name,t.description,t.input_schema,t.schema_fingerprint,t.schema_status,t.disabled_reason,(c.status='active' AND c.revoked_at IS NULL AND t.schema_status='valid' AND t.removed_at IS NULL),c.endpoint_url,c.bearer_ciphertext,c.bearer_nonce,(c.status='active' AND c.revoked_at IS NULL)`

func scanMCPAgentTool(row scanner, item *MCPAgentToolBinding) error {
	return row.Scan(&item.AgentID, &item.ConnectionID, &item.ConnectionName, &item.ConnectionProvider, &item.RemoteToolID, &item.RemoteName, &item.StableName, &item.Description, &item.InputSchema, &item.SchemaFingerprint, &item.SchemaStatus, &item.DisabledReason, &item.Enabled, &item.EndpointURL, &item.BearerCipher, &item.BearerNonce, &item.ConnectionUp)
}

func (db *Database) PersonalAgentMCPTools(ctx context.Context, userID, agentID string) ([]MCPAgentToolBinding, error) {
	if _, err := db.AskIdentityByID(ctx, userID, agentID); err != nil {
		return nil, err
	}
	items := []MCPAgentToolBinding{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+mcpAgentToolColumns+` FROM misty_ask_identities a CROSS JOIN mcp_remote_connections c JOIN mcp_remote_tools t ON t.connection_id=c.id WHERE a.id=$1 AND a.owner_user_id=$2 AND a.deleted_at IS NULL AND a.enabled AND c.owner_user_id=$2 AND c.revoked_at IS NULL AND t.removed_at IS NULL ORDER BY lower(c.name),t.remote_name`, agentID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MCPAgentToolBinding
			if err := scanMCPAgentTool(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SetPersonalAgentMCPTools(ctx context.Context, userID, agentID string, selections []MCPAgentToolSelection) ([]MCPAgentToolBinding, error) {
	// Compatibility endpoint: tools follow account connections automatically.
	return db.PersonalAgentMCPTools(ctx, userID, agentID)
}

func (db *Database) EnabledPersonalAgentMCPTools(ctx context.Context, userID, agentID string) ([]MCPAgentToolBinding, error) {
	items, err := db.PersonalAgentMCPTools(ctx, userID, agentID)
	if err != nil {
		return nil, err
	}
	out := items[:0]
	for _, item := range items {
		if item.Enabled && item.ConnectionUp && item.SchemaStatus == "valid" {
			out = append(out, item)
		}
	}
	return out, nil
}

func (db *Database) PersonalAgentMCPToolForExecution(ctx context.Context, userID, agentID, stableName string) (*MCPAgentToolBinding, error) {
	item := &MCPAgentToolBinding{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanMCPAgentTool(tx.QueryRowContext(ctx, `SELECT `+mcpAgentToolColumns+` FROM misty_ask_identities a CROSS JOIN mcp_remote_connections c JOIN mcp_remote_tools t ON t.connection_id=c.id WHERE a.id=$1 AND a.owner_user_id=$2 AND a.deleted_at IS NULL AND a.enabled AND t.stable_name=$3 AND c.owner_user_id=$2 AND c.status='active' AND c.revoked_at IS NULL AND t.schema_status='valid' AND t.removed_at IS NULL`, agentID, userID, stableName), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}

func (db *Database) RecordMCPExecutionAudit(ctx context.Context, item MCPExecutionAudit) error {
	item.ID = "mcp_execution_" + uuid.NewString()
	if item.OwnerUserID == "" || item.AgentID == "" || item.ConnectionID == "" || item.RemoteName == "" || item.StableName == "" || item.IdempotencyKey == "" || item.Source == "" || item.DurationMS < 0 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var provenanceValid bool
		if err := tx.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)
			AND EXISTS(SELECT 1 FROM mcp_remote_connections WHERE id=$3 AND owner_user_id=$2)
			AND ($4='' OR EXISTS(SELECT 1 FROM mcp_remote_tools WHERE id=$4 AND connection_id=$3))`, item.AgentID, item.OwnerUserID, item.ConnectionID, item.RemoteToolID).Scan(&provenanceValid); err != nil {
			return err
		}
		if !provenanceValid {
			return ErrSpaceInvalid
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_tool_execution_audit(id,owner_user_id,agent_id,connection_id,remote_tool_id,remote_name,stable_name,run_id,idempotency_key,source,approved,success,error_code,duration_ms) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),$9,$10,$11,$12,$13,$14) ON CONFLICT(owner_user_id,idempotency_key) DO NOTHING`, item.ID, item.OwnerUserID, item.AgentID, item.ConnectionID, item.RemoteToolID, item.RemoteName, item.StableName, item.RunID, item.IdempotencyKey, item.Source, item.Approved, item.Success, item.ErrorCode, item.DurationMS)
		return err
	})
}

func (db *Database) MCPExecutionAudits(ctx context.Context, userID, agentID string, limit int) ([]MCPExecutionAudit, error) {
	if _, err := db.AskIdentityByID(ctx, userID, agentID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []MCPExecutionAudit{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT e.id,e.agent_id,e.connection_id,COALESCE(e.remote_tool_id,''),e.remote_name,e.stable_name,COALESCE(e.run_id,''),e.source,e.approved,e.success,e.error_code,e.duration_ms,e.created_at FROM mcp_tool_execution_audit e JOIN misty_ask_identities a ON a.id=e.agent_id WHERE e.owner_user_id=$1 AND e.agent_id=$2 AND a.owner_user_id=$1 ORDER BY e.created_at DESC LIMIT $3`, userID, agentID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item MCPExecutionAudit
			if err := rows.Scan(&item.ID, &item.AgentID, &item.ConnectionID, &item.RemoteToolID, &item.RemoteName, &item.StableName, &item.RunID, &item.Source, &item.Approved, &item.Success, &item.ErrorCode, &item.DurationMS, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
