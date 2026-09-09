package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type AgentRuntimeStartReceipt struct {
	Claimed      bool   `json:"claimed"`
	ClaimToken   string `json:"claim_token,omitempty"`
	RuntimeRunID string `json:"runtime_run_id,omitempty"`
}

// AgentRuntimeStartReceipt is service-only. A claim is never leased or reclaimed:
// once engine submission may have happened, a second submission is unsafe.
// Activation's committed runtime binding also serves as a receipt when the
// adapter crashed before recording the engine response.
func (db *Database) AgentRuntimeStartReceipt(ctx context.Context, runID, adapter, callback, claim, runtimeID string) (*AgentRuntimeStartReceipt, error) {
	table := "space_runs"
	if strings.HasPrefix(runID, "invocation_") {
		table = "ai_invocations"
	} else if !strings.HasPrefix(runID, "run_") {
		return nil, ErrSpaceInvalid
	}
	if adapter != BetaAgentRuntimeAdapter || callback == "" || (claim == "") != (runtimeID == "") || len(runtimeID) > 512 {
		return nil, ErrSpaceInvalid
	}
	out := &AgentRuntimeStartReceipt{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var state, bound, pinnedAdapter, pinnedCallback string
		if err := tx.QueryRowContext(ctx, `SELECT state,runtime_run_id,runtime_adapter_version,COALESCE(runtime_callback_endpoint,'') FROM `+table+` WHERE id=$1 FOR UPDATE`, runID).Scan(&state, &bound, &pinnedAdapter, &pinnedCallback); err != nil {
			return err
		}
		if pinnedAdapter != adapter || pinnedCallback != callback {
			return ErrSpaceForbidden
		}
		var token, recorded string
		err := tx.QueryRowContext(ctx, `SELECT claim_token,runtime_run_id FROM agent_runtime_start_receipts WHERE run_id=$1`, runID).Scan(&token, &recorded)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if claim != "" {
			if token != claim || (recorded != "" && recorded != runtimeID) || (bound != "" && bound != runtimeID) {
				return ErrSpaceConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agent_runtime_start_receipts SET runtime_run_id=$2,updated_at=NOW() WHERE run_id=$1`, runID, runtimeID); err != nil {
				return err
			}
			out.RuntimeRunID = runtimeID
			return nil
		}
		if bound != "" {
			out.RuntimeRunID = bound
			return nil
		}
		if recorded != "" {
			out.RuntimeRunID = recorded
			return nil
		}
		if state != "queued" && state != "running" {
			return ErrSpaceConflict
		}
		if token != "" {
			return nil
		} // Pending/uncertain, never permission to start again.
		out.ClaimToken = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_runtime_start_receipts(run_id,claim_token) VALUES($1,$2)`, runID, out.ClaimToken); err != nil {
			return err
		}
		out.Claimed = true
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}
