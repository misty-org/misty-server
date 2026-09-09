package db

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrPersonalAgentNotFound = errors.New("personal agent not found")
	ErrPersonalAgentConflict = errors.New("personal agent version conflict")
	ErrPersonalAgentModel    = errors.New("personal agent model unavailable")
)

type AskIdentity struct {
	ID              string          `json:"id"`
	OwnerUserID     string          `json:"owner_user_id"`
	Name            string          `json:"name"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar"`
	Instructions    string          `json:"instructions,omitempty"`
	ModelMode       string          `json:"model_mode"`
	ModelID         string          `json:"model_id,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	DefaultRunMode  string          `json:"default_run_mode"`
	VoiceID         string          `json:"voice_id"`
	Enabled         bool            `json:"enabled"`
	SystemManaged   bool            `json:"system_managed,omitempty"`
	Version         int64           `json:"version"`
	LatestVersionID string          `json:"latest_version_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AskIdentityVersion struct {
	ID              string          `json:"id"`
	AgentID         string          `json:"agent_id"`
	Version         int64           `json:"version"`
	Name            string          `json:"name"`
	Role            string          `json:"role"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Avatar          json.RawMessage `json:"avatar"`
	Instructions    string          `json:"instructions,omitempty"`
	ModelMode       string          `json:"model_mode"`
	ModelID         string          `json:"model_id,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	DefaultRunMode  string          `json:"default_run_mode"`
	VoiceID         string          `json:"voice_id"`
	ChecksumSHA256  string          `json:"checksum_sha256"`
	CreatedByUserID string          `json:"created_by_user_id"`
	CreatedAt       time.Time       `json:"created_at"`
}

const personalAgentColumns = `id,owner_user_id,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,default_run_mode,voice_id,enabled,system_managed,version,created_at,updated_at`

func scanPersonalAgent(row scanner, out *AskIdentity) error {
	err := row.Scan(&out.ID, &out.OwnerUserID, &out.Name, &out.Role, &out.Description, &out.Icon, &out.Avatar, &out.Instructions,
		&out.ModelMode, &out.ModelID, &out.ReasoningEffort, &out.DefaultRunMode, &out.VoiceID, &out.Enabled,
		&out.SystemManaged, &out.Version, &out.CreatedAt, &out.UpdatedAt)
	if err == nil {
		out.LatestVersionID = personalAgentVersionID(out.ID, out.Version)
	}
	return err
}

func personalAgentVersionID(agentID string, version int64) string {
	return fmt.Sprintf("personalver_%x", md5.Sum([]byte(fmt.Sprintf("%s:%d", agentID, version))))
}

func insertPersonalAgentVersionTx(ctx context.Context, tx *sql.Tx, agent AskIdentity, userID string) (string, error) {
	id := personalAgentVersionID(agent.ID, agent.Version)
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", agent.ID, agent.Version, agent.Name, agent.Role, agent.Description, agent.Instructions, agent.ModelID, agent.ReasoningEffort, agent.Icon, string(agent.Avatar), agent.DefaultRunMode, agent.VoiceID))))
	_, err := tx.ExecContext(ctx, `INSERT INTO misty_ask_identity_versions(id,agent_id,version,name,role,description,icon,avatar,instructions,model_mode,model_id,reasoning_effort,default_run_mode,voice_id,checksum_sha256,created_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(agent_id,version) DO NOTHING`, id, agent.ID, agent.Version, agent.Name, agent.Role, agent.Description, agent.Icon, agent.Avatar, agent.Instructions, agent.ModelMode, agent.ModelID, agent.ReasoningEffort, agent.DefaultRunMode, agent.VoiceID, checksum, userID)
	return id, err
}

func validPersonalJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func (db *Database) AskIdentityByID(ctx context.Context, userID, agentID string) (*AskIdentity, error) {
	out := &AskIdentity{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		err := scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND system_managed AND deleted_at IS NULL`, agentID, userID), out)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		}
		return err
	})
	return out, err
}

func cancelPersonalAgentRunsTx(ctx context.Context, tx *sql.Tx, agentID, code string) error {
	if _, err := tx.ExecContext(ctx, `WITH canceled AS (
		UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code=$2,canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
		WHERE agent_id=$1 AND state IN ('queued','running','cooldown','awaiting_approval','awaiting_device','awaiting_intervention') RETURNING id
	) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW()
	WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, agentID, code); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='denied',decided_at=NOW()
		WHERE run_id IN (SELECT id FROM space_runs WHERE agent_id=$1 AND state='canceled' AND error_code=$2) AND state='pending'`, agentID, code); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW()
		WHERE run_id IN (SELECT id FROM space_runs WHERE agent_id=$1 AND state='canceled' AND error_code=$2) AND state='attached'`, agentID, code)
	return err
}

func cancelCreatorSpaceRunsTx(ctx context.Context, tx *sql.Tx, ownerUserID, spaceID, code string) error {
	if _, err := tx.ExecContext(ctx, `WITH canceled AS (
		UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code=$3,canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
		WHERE owner_user_id=$1 AND space_id=$2 AND state IN ('queued','running','cooldown','awaiting_approval','awaiting_device','awaiting_intervention') RETURNING id
	) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW()
	WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, ownerUserID, spaceID, code); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='denied',decided_at=NOW()
		WHERE run_id IN (SELECT id FROM space_runs WHERE owner_user_id=$1 AND space_id=$2 AND state='canceled' AND error_code=$3) AND state='pending'`, ownerUserID, spaceID, code); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW()
		WHERE run_id IN (SELECT id FROM space_runs WHERE owner_user_id=$1 AND space_id=$2 AND state='canceled' AND error_code=$3) AND state='attached'`, ownerUserID, spaceID, code)
	return err
}
