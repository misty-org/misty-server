package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// EnsureManagedMistyAgent returns the single server-managed runtime identity
// behind the user-facing Misty. The identity is implementation detail: users
// cannot configure its model, instructions, permissions, or run mode.
func (db *Database) EnsureManagedMistyAgent(ctx context.Context, userID, modelID string) (*PersonalAgent, error) {
	userID, modelID = strings.TrimSpace(userID), strings.TrimSpace(modelID)
	if userID == "" || modelID == "" {
		return nil, ErrSpaceInvalid
	}
	out := &PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Serialize lazy creation without relying on a read-then-insert race.
		var lockResult any
		if err := tx.QueryRowContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "managed-misty:"+userID).Scan(&lockResult); err != nil {
			return err
		}
		err := scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents
			WHERE owner_user_id=$1 AND system_managed AND deleted_at IS NULL`, userID), out)
		if err == nil {
			if !out.Enabled || out.ModelID != modelID || out.Name != "Misty" || out.DefaultRunMode != "auto" {
				if err := scanPersonalAgent(tx.QueryRowContext(ctx, `UPDATE personal_agents SET name='Misty',role='Assistant',description=$1,
					instructions=$2,model_mode='pinned',model_id=$3,reasoning_effort='',default_run_mode='auto',enabled=TRUE,
					version=version+1,updated_at=NOW() WHERE id=$4 RETURNING `+personalAgentColumns,
					managedMistyDescription, managedMistyInstructions, modelID, out.ID), out); err != nil {
					return err
				}
				if _, err = insertPersonalAgentVersionTx(ctx, tx, *out, userID); err != nil {
					return err
				}
			}
			return syncManagedMistyMCPToolsTx(ctx, tx, userID, out.ID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		avatar, _ := json.Marshal(map[string]any{"kind": "preset", "preset_id": "misty", "accent": "blue"})
		out.ID = "personal_misty_" + uuid.NewString()
		if err := scanPersonalAgent(tx.QueryRowContext(ctx, `INSERT INTO personal_agents(
			id,owner_user_id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,
			default_run_mode,voice_id,enabled,system_managed)
			VALUES($1,$2,'Misty','Assistant',$3,'misty',$4,$5,'pinned',$6,'','auto','alloy',TRUE,TRUE)
			RETURNING `+personalAgentColumns, out.ID, userID, managedMistyDescription, avatar, managedMistyInstructions, modelID), out); err != nil {
			return err
		}
		if _, err = insertPersonalAgentVersionTx(ctx, tx, *out, userID); err != nil {
			return err
		}
		return syncManagedMistyMCPToolsTx(ctx, tx, userID, out.ID)
	})
	return out, err
}

// Connecting an MCP server is the single user-facing grant for managed Misty.
// Valid tools on active connections are synchronized automatically; the
// runtime still applies its normal per-call approval and audit policy.
func syncManagedMistyMCPToolsTx(ctx context.Context, tx *sql.Tx, userID, agentID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT c.id,t.id,t.stable_name,t.schema_fingerprint
		FROM mcp_remote_connections c JOIN mcp_remote_tools t ON t.connection_id=c.id
		WHERE c.owner_user_id=$1 AND c.status='active' AND c.revoked_at IS NULL
			AND t.schema_status='valid' AND t.removed_at IS NULL`, userID)
	if err != nil {
		return err
	}
	type tool struct{ connectionID, remoteID, stableName, fingerprint string }
	tools := []tool{}
	for rows.Next() {
		var item tool
		if err := rows.Scan(&item.connectionID, &item.remoteID, &item.stableName, &item.fingerprint); err != nil {
			rows.Close()
			return err
		}
		tools = append(tools, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range tools {
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_agent_mcp_tools(
			id,owner_user_id,agent_id,connection_id,remote_tool_id,stable_name,schema_fingerprint,enabled)
			VALUES($1,$2,$3,$4,$5,$6,$7,TRUE)
			ON CONFLICT(agent_id,connection_id,remote_tool_id) DO UPDATE SET
				stable_name=EXCLUDED.stable_name,schema_fingerprint=EXCLUDED.schema_fingerprint,
				enabled=TRUE,updated_at=NOW()`, "mcp_binding_"+uuid.NewString(), userID, agentID,
			item.connectionID, item.remoteID, item.stableName, item.fingerprint); err != nil {
			return err
		}
	}
	return nil
}

const managedMistyDescription = "Misty's managed background runtime"

const managedMistyInstructions = `You are Misty, the user's single assistant across the Misty application. Complete the user's request with the least surprising authorized actions. Read and perform routine internal work automatically. Pause for creator approval before consequential external, destructive, security-sensitive, paid, or device actions. You may delegate bounded independent subtasks to hidden workers, but remain responsible for their results. Never expose internal worker identities as separate assistants.`
