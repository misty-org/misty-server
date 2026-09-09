package db

import (
	"context"
	"database/sql"
	"strings"
)

const BetaAgentRuntimeAdapter = "vercel-workflow/1"

type AgentRuntimePin struct {
	AdapterVersion   string
	Endpoint         string
	CallbackEndpoint string
}

// BindAgentRuntime pins trusted deployment configuration before any network
// delivery. Starts, resumes, recovery and cancellation all use this same binding.
// It is an internal control-plane operation, never an app registration API.
func (db *Database) BindAgentRuntime(ctx context.Context, runID, endpoint, callback string) (*AgentRuntimePin, error) {
	if endpoint == "" || callback == "" {
		return nil, ErrAppRuntimeForbidden
	}
	table := "space_runs"
	if strings.HasPrefix(runID, "invocation_") {
		table = "ai_invocations"
	} else if !strings.HasPrefix(runID, "run_") {
		return nil, ErrSpaceInvalid
	}
	pin := &AgentRuntimePin{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE `+table+` SET runtime_endpoint=COALESCE(runtime_endpoint,$2),runtime_callback_endpoint=COALESCE(runtime_callback_endpoint,$3) WHERE id=$1 RETURNING runtime_adapter_version,runtime_endpoint,runtime_callback_endpoint`, runID, endpoint, callback).Scan(&pin.AdapterVersion, &pin.Endpoint, &pin.CallbackEndpoint)
	})
	return pin, err
}
