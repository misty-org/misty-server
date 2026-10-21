package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// agentToolboxClaim reports whether this call inserted the journal row or found an existing one.
type agentToolboxClaim struct {
	claimed  bool
	existing agentToolboxJournalRow
}

// agentToolboxJournalRow is the journal state read back for a call whose key already exists.
type agentToolboxJournalRow struct {
	state, user, tool, fingerprint, errorCode, risk, run string
	result                                               json.RawMessage
	ciphertext                                           []byte
}

// claimAgentToolboxAction runs the claim transaction: it inserts the journal row or loads the existing one.
func (db *Database) claimAgentToolboxAction(ctx context.Context, action *AgentToolboxAction, fingerprint string, persistedRequest json.RawMessage) (agentToolboxClaim, error) {
	var claim agentToolboxClaim
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Reading never adds an effect, and looking is how an uncertain one is reconciled.
		if action.RequireSettledRun && action.Risk != "read" {
			if err := requireSettledAgentToolboxRun(ctx, tx, action); err != nil {
				return err
			}
		}
		if action.LegacyIdempotencyKey != "" {
			if err := resolveLegacyAgentToolboxKey(ctx, tx, action); err != nil {
				return err
			}
		}
		inserted, err := insertAgentToolboxJournalRow(ctx, tx, action, fingerprint, persistedRequest)
		if err != nil {
			return err
		}
		if inserted {
			claim.claimed = true
			return nil
		}
		existing, err := loadExistingAgentToolboxRow(ctx, tx, action, fingerprint)
		if err != nil {
			return err
		}
		claim.existing = existing
		// Retrying writes without adapter reconciliation could repeat a committed effect.
		if existing.state == "failed" && (existing.errorCode == "tool_not_attempted" || action.Risk == "read" && !action.RedactPayload) {
			reclaimed, err := reclaimFailedAgentToolboxRow(ctx, tx, action, persistedRequest, fingerprint)
			if err != nil {
				return err
			}
			claim.claimed = reclaimed
		}
		return nil
	})
	return claim, err
}

// harmlessPageAction matches a browser page action that was not marked consequential: a
// click, toggle or typing in the agent's own page. Sends, posts, payments and deletions
// need allowConsequential (the page layer refuses them otherwise), so an uncertain
// outcome here cannot have done one and need not hold up the rest of the run.
const harmlessPageAction = `tool_name LIKE 'browser.%' AND tool_name NOT LIKE 'browser.workspace.%'
		AND NOT jsonb_path_exists(COALESCE(request,'{}'::jsonb), 'lax $.**.allowConsequential ? (@ == true)')`

// requireSettledAgentToolboxRun locks the run and rejects the action while an earlier effect is unconfirmed.
func requireSettledAgentToolboxRun(ctx context.Context, tx *sql.Tx, action *AgentToolboxAction) error {
	var state string
	query := `SELECT state FROM space_runs WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`
	if strings.HasPrefix(action.RunID, "invocation_") {
		query = `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, query, action.RunID, action.UserID).Scan(&state); err != nil {
		return err
	}
	if state != "running" {
		return ErrSpaceConflict
	}
	var pending string
	err := tx.QueryRowContext(ctx, `SELECT idempotency_key FROM agent_toolbox_action_journal WHERE run_id=$1 AND user_id=$2 AND idempotency_key<>$3 AND risk<>'read' AND (state IN ('started','unknown') OR (state='failed' AND COALESCE(error_code,'')<>'tool_not_attempted'))
		AND NOT (`+harmlessPageAction+`) ORDER BY created_at,idempotency_key LIMIT 1`, action.RunID, action.UserID, action.IdempotencyKey).Scan(&pending)
	if err == nil {
		return &PendingRunEffect{IdempotencyKey: pending}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// resolveLegacyAgentToolboxKey switches to an existing row stored under the legacy key, if one exists.
func resolveLegacyAgentToolboxKey(ctx context.Context, tx *sql.Tx, action *AgentToolboxAction) error {
	var found string
	lookupErr := tx.QueryRowContext(ctx, `SELECT idempotency_key FROM agent_toolbox_action_journal WHERE idempotency_key IN ($1,$2) ORDER BY (idempotency_key=$1) DESC LIMIT 1`, action.IdempotencyKey, action.LegacyIdempotencyKey).Scan(&found)
	if lookupErr == nil {
		action.IdempotencyKey = found
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	return nil
}

// insertAgentToolboxJournalRow inserts a started row and reports whether it was new.
func insertAgentToolboxJournalRow(ctx context.Context, tx *sql.Tx, action *AgentToolboxAction, fingerprint string, persistedRequest json.RawMessage) (bool, error) {
	inserted, err := tx.ExecContext(ctx, `INSERT INTO agent_toolbox_action_journal(
   idempotency_key,user_id,space_id,agent_id,agent_instance_id,run_id,session_id,tool_name,audit_event,risk,source,request,state,request_fingerprint
  ) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,'started',$13) ON CONFLICT DO NOTHING`,
		action.IdempotencyKey, action.UserID, action.SpaceID, action.AgentID, action.AgentInstanceID, action.RunID, action.SessionID, action.ToolName, action.AuditEvent, action.Risk, action.Source, persistedRequest, fingerprint)
	if err != nil {
		return false, err
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// loadExistingAgentToolboxRow locks the existing row and verifies it belongs to the same action.
func loadExistingAgentToolboxRow(ctx context.Context, tx *sql.Tx, action *AgentToolboxAction, fingerprint string) (agentToolboxJournalRow, error) {
	var row agentToolboxJournalRow
	err := tx.QueryRowContext(ctx, `SELECT state,result,user_id,tool_name,COALESCE(request_fingerprint,''),result_ciphertext,COALESCE(error_code,''),risk,COALESCE(run_id,'') FROM agent_toolbox_action_journal WHERE idempotency_key=$1 FOR UPDATE`, action.IdempotencyKey).Scan(&row.state, &row.result, &row.user, &row.tool, &row.fingerprint, &row.ciphertext, &row.errorCode, &row.risk, &row.run)
	if err != nil {
		return row, err
	}
	if row.user != action.UserID || row.tool != action.ToolName || row.run != action.RunID || row.risk != action.Risk {
		return row, ErrSpaceForbidden
	}
	if row.fingerprint != "" && row.fingerprint != fingerprint {
		return row, ErrSpaceConflict
	}
	return row, nil
}

// reclaimFailedAgentToolboxRow moves a failed row back to started and reports whether the move happened.
func reclaimFailedAgentToolboxRow(ctx context.Context, tx *sql.Tx, action *AgentToolboxAction, persistedRequest json.RawMessage, fingerprint string) (bool, error) {
	updated, err := tx.ExecContext(ctx, `UPDATE agent_toolbox_action_journal SET state='started',request=$1,request_fingerprint=$2,result='{}'::jsonb,error_code=NULL,updated_at=NOW() WHERE idempotency_key=$3 AND state='failed'`, persistedRequest, fingerprint, action.IdempotencyKey)
	if err != nil {
		return false, err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
