package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
var ErrAgentToolboxActionUnknown = errors.New("Agent Toolbox action outcome is uncertain; reconcile before retrying")
var ErrAgentToolboxNotAttempted = errors.New("Agent Toolbox action was not dispatched")

// PendingRunEffect identifies the original effect that prevents a new action.
// Returning it before insertion prevents a replanner from minting a replacement.
type PendingRunEffect struct{ IdempotencyKey string }

func (e *PendingRunEffect) Error() string {
	return "The run has an unconfirmed effect; reconcile it before continuing"
}

type AgentToolboxAction struct {
	IdempotencyKey       string
	RequireSettledRun    bool
	LegacyIdempotencyKey string
	UserID               string
	SpaceID              string
	AgentID              string
	AgentInstanceID      string
	RunID                string
	SessionID            string
	ToolName             string
	AuditEvent           string
	Risk                 string
	Source               string
	Request              json.RawMessage
	RedactPayload        bool
	ProtectResult        func(json.RawMessage) ([]byte, error)
	RestoreResult        func([]byte) (json.RawMessage, error)
}

func (db *Database) JournalAgentToolboxAction(ctx context.Context, action AgentToolboxAction, execute func() (json.RawMessage, error)) (json.RawMessage, error) {
	action.IdempotencyKey = strings.TrimSpace(action.IdempotencyKey)
	action.UserID = strings.TrimSpace(action.UserID)
	action.ToolName = strings.TrimSpace(action.ToolName)
	if action.IdempotencyKey == "" || action.UserID == "" || action.ToolName == "" || action.AuditEvent == "" || (action.Risk != "read" && action.Risk != "write" && action.Risk != "dangerous") || execute == nil || !validJSONObject(action.Request) {
		return nil, ErrSpaceInvalid
	}
	if action.RedactPayload && (action.ProtectResult == nil || action.RestoreResult == nil) {
		return nil, ErrSpaceInvalid
	}
	// Canonical JSON prevents harmless object-key reordering from changing identity.
	var requestValue any
	if json.Unmarshal(action.Request, &requestValue) != nil {
		return nil, ErrSpaceInvalid
	}
	canonical, err := json.Marshal(requestValue)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(hash[:])
	persistedRequest := action.Request
	if action.RedactPayload {
		persistedRequest = json.RawMessage(`{}`)
	}
	claimed := false
	var existingState, existingUser, existingTool, existingFingerprint, existingError, existingRisk, existingRun string
	var existingResult json.RawMessage
	var existingCiphertext []byte
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if action.RequireSettledRun {
			var state string
			query := `SELECT state FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
			if strings.HasPrefix(action.RunID, "invocation_") {
				query = `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`
			}
			if err := tx.QueryRowContext(ctx, query, action.RunID, action.UserID).Scan(&state); err != nil {
				return err
			}
			var routineCancelled bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_routine_runs WHERE invocation_id=$1 AND user_id=$2 AND cancel_requested_at IS NOT NULL)`, action.RunID, action.UserID).Scan(&routineCancelled); err != nil {
				return err
			}
			if routineCancelled {
				return ErrSpaceConflict
			}
			if state != "running" {
				return ErrSpaceConflict
			}
			if err := routineAgentEffectClaimTx(ctx, tx, action); err != nil {
				return err
			}
			var pending string
			err := tx.QueryRowContext(ctx, `SELECT idempotency_key FROM agent_toolbox_action_journal WHERE run_id=$1 AND user_id=$2 AND idempotency_key<>$3 AND risk<>'read' AND (state IN ('started','unknown') OR (state='failed' AND COALESCE(error_code,'')<>'tool_not_attempted')) ORDER BY created_at,idempotency_key LIMIT 1`, action.RunID, action.UserID, action.IdempotencyKey).Scan(&pending)
			if err == nil {
				return &PendingRunEffect{IdempotencyKey: pending}
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if action.LegacyIdempotencyKey != "" {
			var found string
			lookupErr := tx.QueryRowContext(ctx, `SELECT idempotency_key FROM agent_toolbox_action_journal WHERE idempotency_key IN ($1,$2) ORDER BY (idempotency_key=$1) DESC LIMIT 1`, action.IdempotencyKey, action.LegacyIdempotencyKey).Scan(&found)
			if lookupErr == nil {
				action.IdempotencyKey = found
			} else if !errors.Is(lookupErr, sql.ErrNoRows) {
				return lookupErr
			}
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO agent_toolbox_action_journal(
   idempotency_key,user_id,space_id,agent_id,agent_instance_id,run_id,session_id,tool_name,audit_event,risk,source,request,state,request_fingerprint
  ) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,'started',$13) ON CONFLICT DO NOTHING`,
			action.IdempotencyKey, action.UserID, action.SpaceID, action.AgentID, action.AgentInstanceID, action.RunID, action.SessionID, action.ToolName, action.AuditEvent, action.Risk, action.Source, persistedRequest, fingerprint)
		if err != nil {
			return err
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		if count == 1 {
			claimed = true
			return nil
		}
		err = tx.QueryRowContext(ctx, `SELECT state,result,user_id,tool_name,COALESCE(request_fingerprint,''),result_ciphertext,COALESCE(error_code,''),risk,COALESCE(run_id,'') FROM agent_toolbox_action_journal WHERE idempotency_key=$1 FOR UPDATE`, action.IdempotencyKey).Scan(&existingState, &existingResult, &existingUser, &existingTool, &existingFingerprint, &existingCiphertext, &existingError, &existingRisk, &existingRun)
		if err != nil {
			return err
		}
		if existingUser != action.UserID || existingTool != action.ToolName || existingRun != action.RunID || existingRisk != action.Risk {
			return ErrSpaceForbidden
		}
		if existingFingerprint != "" && existingFingerprint != fingerprint {
			return ErrSpaceConflict
		}
		// Retrying writes without adapter reconciliation could repeat a committed effect.
		if existingState == "failed" && (existingError == "tool_not_attempted" || action.Risk == "read" && !action.RedactPayload) {
			updated, err := tx.ExecContext(ctx, `UPDATE agent_toolbox_action_journal SET state='started',request=$1,request_fingerprint=$2,result='{}'::jsonb,error_code=NULL,updated_at=NOW() WHERE idempotency_key=$3 AND state='failed'`, persistedRequest, fingerprint, action.IdempotencyKey)
			if err != nil {
				return err
			}
			count, err := updated.RowsAffected()
			claimed = count == 1
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		switch existingState {
		case "completed":
			if !action.RedactPayload {
				return existingResult, nil
			}
			if len(existingCiphertext) == 0 {
				return nil, ErrAgentToolboxActionUnknown
			}
			restored, err := action.RestoreResult(existingCiphertext)
			if err != nil || !json.Valid(restored) {
				return nil, errors.Join(ErrAgentToolboxActionUnknown, err)
			}
			return restored, nil
		case "unknown":
			return nil, ErrAgentToolboxActionUnknown
		case "failed":
			return nil, ErrAgentToolboxActionTerminal
		default:
			return nil, ErrAgentToolboxActionInProgress
		}
	}
	result, executeErr := execute()
	if len(result) == 0 || !json.Valid(result) {
		result = json.RawMessage(`{}`)
		if executeErr == nil {
			executeErr = errors.New("tool returned no valid execution result")
		}
	}
	state, errorCode := "completed", ""
	if executeErr != nil {
		state, errorCode = "failed", "tool_execution_failed"
		if action.Risk != "read" {
			state, errorCode = "unknown", "tool_outcome_unknown"
		}
		if errors.Is(executeErr, ErrAgentToolboxNotAttempted) {
			state, errorCode = "failed", "tool_not_attempted"
		}
	}
	persistedResult := result
	var ciphertext []byte
	if action.RedactPayload {
		persistedResult = json.RawMessage(`{}`)
		if executeErr == nil {
			ciphertext, err = action.ProtectResult(result)
			if err != nil {
				return nil, errors.Join(ErrAgentToolboxActionUnknown, err)
			}
		}
	}
	// A cancelled transport must not prevent recording an already committed effect.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err = db.TestingSpaceTx(finishCtx, func(tx *sql.Tx) error {
		updated, err := tx.ExecContext(finishCtx, `UPDATE agent_toolbox_action_journal SET state=$1,result=$2,error_code=NULLIF($3,''),result_ciphertext=$6,updated_at=NOW() WHERE idempotency_key=$4 AND user_id=$5 AND state='started'`, state, persistedResult, errorCode, action.IdempotencyKey, action.UserID, ciphertext)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err == nil && count != 1 {
			return ErrAgentToolboxActionUnknown
		}
		return err
	})
	if err != nil {
		return nil, errors.Join(ErrAgentToolboxActionUnknown, err)
	}
	if state == "unknown" {
		return nil, errors.Join(ErrAgentToolboxActionUnknown, executeErr)
	}
	return result, executeErr
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
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`, agentID, userID).Scan(&exists); err != nil {
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
