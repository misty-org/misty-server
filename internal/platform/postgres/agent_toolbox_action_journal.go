package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AgentToolboxActionAudit struct {
	ToolName   string    `json:"tool_name"`
	AuditEvent string    `json:"audit_event"`
	Risk       string    `json:"risk"`
	Source     string    `json:"source"`
	State      string    `json:"state"`
	ErrorCode  string    `json:"error_code,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var ErrAgentToolboxActionInProgress = errors.New("Agent Toolbox action is already in progress")
var ErrAgentToolboxActionTerminal = errors.New("Agent Toolbox action already attempted")

type AgentToolboxAction struct {
	IdempotencyKey  string
	UserID          string
	SpaceID         string
	AgentID         string
	AgentInstanceID string
	RunID           string
	SessionID       string
	ToolName        string
	AuditEvent      string
	Risk            string
	Source          string
	Request         json.RawMessage
	RedactPayload   bool
}

func (db *Database) JournalAgentToolboxAction(ctx context.Context, action AgentToolboxAction, execute func() (json.RawMessage, error)) (json.RawMessage, error) {
	action.IdempotencyKey = strings.TrimSpace(action.IdempotencyKey)
	action.UserID = strings.TrimSpace(action.UserID)
	action.ToolName = strings.TrimSpace(action.ToolName)
	action.AuditEvent = strings.TrimSpace(action.AuditEvent)
	action.Source = strings.TrimSpace(action.Source)
	if action.IdempotencyKey == "" || action.UserID == "" || action.ToolName == "" || action.AuditEvent == "" || action.Risk != "read" && action.Risk != "write" && action.Risk != "dangerous" || execute == nil || !validJSONObject(action.Request) {
		return nil, ErrSpaceInvalid
	}

	claimed := false
	var existingState string
	var existingResult json.RawMessage
	var existingUserID string
	persistedRequest := action.Request
	if action.RedactPayload {
		persistedRequest = json.RawMessage(`{}`)
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO agent_toolbox_action_journal(
			idempotency_key,user_id,space_id,agent_id,agent_instance_id,run_id,session_id,tool_name,audit_event,risk,source,request,state
		) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,'started') ON CONFLICT DO NOTHING`,
			action.IdempotencyKey, action.UserID, action.SpaceID, action.AgentID, action.AgentInstanceID, action.RunID, action.SessionID,
			action.ToolName, action.AuditEvent, action.Risk, action.Source, persistedRequest)
		if insertErr != nil {
			return insertErr
		}
		if rows, _ := result.RowsAffected(); rows == 1 {
			claimed = true
			return nil
		}
		if lookupErr := tx.QueryRowContext(ctx, `SELECT state,result,user_id FROM agent_toolbox_action_journal WHERE idempotency_key=$1`, action.IdempotencyKey).Scan(&existingState, &existingResult, &existingUserID); lookupErr != nil {
			return lookupErr
		}
		// The idempotency key includes the acting user. A collision with a row
		// owned by anyone else must never reveal or replay that action.
		if existingUserID != action.UserID {
			return ErrSpaceForbidden
		}
		if existingState == "failed" && !action.RedactPayload {
			update, updateErr := tx.ExecContext(ctx, `UPDATE agent_toolbox_action_journal SET state='started',request=$1,result='{}'::jsonb,error_code=NULL,updated_at=NOW() WHERE idempotency_key=$2 AND user_id=$3 AND state='failed'`, persistedRequest, action.IdempotencyKey, action.UserID)
			if updateErr != nil {
				return updateErr
			}
			rows, _ := update.RowsAffected()
			claimed = rows == 1
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		if existingState == "completed" {
			return existingResult, nil
		}
		if existingState == "failed" && action.RedactPayload {
			return nil, ErrAgentToolboxActionTerminal
		}
		return nil, ErrAgentToolboxActionInProgress
	}

	result, executeErr := execute()
	if len(result) == 0 || !json.Valid(result) {
		result = json.RawMessage(`{}`)
	}
	state, errorCode := "completed", ""
	if executeErr != nil {
		state, errorCode = "failed", "tool_execution_failed"
	}
	persistedResult := result
	if action.RedactPayload {
		persistedResult = json.RawMessage(`{}`)
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, updateErr := tx.ExecContext(ctx, `UPDATE agent_toolbox_action_journal SET state=$1,result=$2,error_code=NULLIF($3,''),updated_at=NOW() WHERE idempotency_key=$4 AND user_id=$5`, state, persistedResult, errorCode, action.IdempotencyKey, action.UserID)
		return updateErr
	})
	if err != nil {
		return nil, err
	}
	return result, executeErr
}

func (db *Database) AgentToolboxActionAudits(ctx context.Context, userID, instanceID string, limit int) ([]AgentToolboxActionAudit, error) {
	userID, instanceID = strings.TrimSpace(userID), strings.TrimSpace(instanceID)
	if userID == "" || instanceID == "" {
		return nil, ErrSpaceInvalid
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []AgentToolboxActionAudit{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agent_instances WHERE id=$1 AND user_id=$2)`, instanceID, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT tool_name,audit_event,risk,source,state,COALESCE(error_code,''),created_at,updated_at
			FROM agent_toolbox_action_journal WHERE user_id=$1 AND agent_instance_id=$2 ORDER BY created_at DESC LIMIT $3`, userID, instanceID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentToolboxActionAudit
			if err := rows.Scan(&item.ToolName, &item.AuditEvent, &item.Risk, &item.Source, &item.State, &item.ErrorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) PersonalAgentToolboxActionAudits(ctx context.Context, userID, agentID string, limit int) ([]AgentToolboxActionAudit, error) {
	userID, agentID = strings.TrimSpace(userID), strings.TrimSpace(agentID)
	if userID == "" || agentID == "" {
		return nil, ErrSpaceInvalid
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := []AgentToolboxActionAudit{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`, agentID, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrPersonalAgentNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT tool_name,audit_event,risk,source,state,COALESCE(error_code,''),created_at,updated_at
			FROM agent_toolbox_action_journal WHERE user_id=$1 AND agent_id=$2 ORDER BY created_at DESC LIMIT $3`, userID, agentID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentToolboxActionAudit
			if err := rows.Scan(&item.ToolName, &item.AuditEvent, &item.Risk, &item.Source, &item.State, &item.ErrorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
