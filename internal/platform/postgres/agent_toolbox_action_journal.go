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
	if err := normalizeAgentToolboxAction(&action, execute); err != nil {
		return nil, err
	}
	fingerprint, err := agentToolboxRequestFingerprint(action.Request)
	if err != nil {
		return nil, err
	}
	persistedRequest := action.Request
	if action.RedactPayload {
		persistedRequest = json.RawMessage(`{}`)
	}
	claim, err := db.claimAgentToolboxAction(ctx, &action, fingerprint, persistedRequest)
	if err != nil {
		return nil, err
	}
	if !claim.claimed {
		return replayAgentToolboxAction(action, claim.existing)
	}
	result, executeErr := execute()
	result, executeErr = normalizeAgentToolboxResult(result, executeErr)
	state, errorCode := classifyAgentToolboxOutcome(action.Risk, executeErr)
	persistedResult, ciphertext, err := protectAgentToolboxResult(action, result, executeErr)
	if err != nil {
		return nil, err
	}
	if err := db.recordAgentToolboxOutcome(ctx, action, state, errorCode, persistedResult, ciphertext); err != nil {
		return nil, errors.Join(ErrAgentToolboxActionUnknown, err)
	}
	if state == "unknown" {
		return nil, errors.Join(ErrAgentToolboxActionUnknown, executeErr)
	}
	return result, executeErr
}

// normalizeAgentToolboxAction trims identifiers in place and rejects malformed actions.
func normalizeAgentToolboxAction(action *AgentToolboxAction, execute func() (json.RawMessage, error)) error {
	action.IdempotencyKey = strings.TrimSpace(action.IdempotencyKey)
	action.UserID = strings.TrimSpace(action.UserID)
	action.ToolName = strings.TrimSpace(action.ToolName)
	if action.IdempotencyKey == "" || action.UserID == "" || action.ToolName == "" || action.AuditEvent == "" || (action.Risk != "read" && action.Risk != "write" && action.Risk != "dangerous") || execute == nil || !validJSONObject(action.Request) {
		return ErrSpaceInvalid
	}
	if action.RedactPayload && (action.ProtectResult == nil || action.RestoreResult == nil) {
		return ErrSpaceInvalid
	}
	return nil
}

// agentToolboxRequestFingerprint hashes the canonical request.
// Canonical JSON prevents harmless object-key reordering from changing identity.
func agentToolboxRequestFingerprint(request json.RawMessage) (string, error) {
	var requestValue any
	if json.Unmarshal(request, &requestValue) != nil {
		return "", ErrSpaceInvalid
	}
	canonical, err := json.Marshal(requestValue)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

// replayAgentToolboxAction answers a call whose journal row already exists without executing the tool.
func replayAgentToolboxAction(action AgentToolboxAction, existing agentToolboxJournalRow) (json.RawMessage, error) {
	switch existing.state {
	case "completed":
		if !action.RedactPayload {
			return existing.result, nil
		}
		if len(existing.ciphertext) == 0 {
			return nil, ErrAgentToolboxActionUnknown
		}
		restored, err := action.RestoreResult(existing.ciphertext)
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

// normalizeAgentToolboxResult replaces an empty or invalid tool result with an empty object.
func normalizeAgentToolboxResult(result json.RawMessage, executeErr error) (json.RawMessage, error) {
	if len(result) == 0 || !json.Valid(result) {
		result = json.RawMessage(`{}`)
		if executeErr == nil {
			executeErr = errors.New("tool returned no valid execution result")
		}
	}
	return result, executeErr
}

// classifyAgentToolboxOutcome maps the tool error to the journal state and error code.
func classifyAgentToolboxOutcome(risk string, executeErr error) (string, string) {
	state, errorCode := "completed", ""
	if executeErr != nil {
		state, errorCode = "failed", "tool_execution_failed"
		if risk != "read" {
			state, errorCode = "unknown", "tool_outcome_unknown"
		}
		if errors.Is(executeErr, ErrAgentToolboxNotAttempted) {
			state, errorCode = "failed", "tool_not_attempted"
		}
	}
	return state, errorCode
}

// protectAgentToolboxResult returns the result to persist and, for redacted actions, its ciphertext.
func protectAgentToolboxResult(action AgentToolboxAction, result json.RawMessage, executeErr error) (json.RawMessage, []byte, error) {
	persistedResult := result
	var ciphertext []byte
	if action.RedactPayload {
		persistedResult = json.RawMessage(`{}`)
		if executeErr == nil {
			var err error
			ciphertext, err = action.ProtectResult(result)
			if err != nil {
				return nil, nil, errors.Join(ErrAgentToolboxActionUnknown, err)
			}
		}
	}
	return persistedResult, ciphertext, nil
}

// recordAgentToolboxOutcome writes the final journal state for a started row.
func (db *Database) recordAgentToolboxOutcome(ctx context.Context, action AgentToolboxAction, state, errorCode string, persistedResult json.RawMessage, ciphertext []byte) error {
	// A cancelled transport must not prevent recording an already committed effect.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return db.TestingSpaceTx(finishCtx, func(tx *sql.Tx) error {
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
}
