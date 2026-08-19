package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentDeviceGrant is retained as a response shape for older internal browser
// helpers. Rows are now synthesized exclusively from run-bound contexts.
type AgentDeviceGrant struct {
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	AgentID      string          `json:"agent_id"`
	SpaceID      string          `json:"space_id"`
	DeviceID     string          `json:"device_id"`
	ScopeID      string          `json:"scope_id"`
	Capabilities json.RawMessage `json:"capabilities"`
	Metadata     json.RawMessage `json:"metadata"`
	ExpiresAt    time.Time       `json:"expires_at"`
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

var deviceAgentCapabilities = map[string]bool{
	"files.read": true, "files.write": true, "files.list": true, "files.search": true, "files.copy": true, "files.move": true, "files.delete": true,
	"project.patch": true, "project.diff": true, "project.status": true, "project.checks": true, "git.commit": true, "git.push": true, "terminal.execute": true,
	"browser.inspect": true, "browser.navigate": true, "browser.click": true, "browser.type": true, "browser.select": true, "browser.scroll": true,
	"browser.downloads.list": true, "browser.upload": true, "browser.confirm_high_risk": true,
}

func normalizeDeviceAgentCapabilities(raw json.RawMessage) (json.RawMessage, error) {
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !deviceAgentCapabilities[value] {
			return nil, ErrSpaceInvalid
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return json.Marshal(out)
}

func (db *Database) AgentDeviceGrants(ctx context.Context, userID, spaceID, agentID string) ([]AgentDeviceGrant, error) {
	items := []AgentDeviceGrant{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.owner_user_id,r.agent_id,c.space_id,c.device_id,c.opaque_ref,c.capabilities,c.metadata,c.expires_at,
			CASE WHEN c.state='attached' THEN NULL ELSE c.updated_at END,c.created_at,c.updated_at FROM agent_run_contexts c JOIN space_runs r ON r.id=c.run_id
			WHERE c.owner_user_id=$1 AND c.space_id=$2 AND r.agent_id=$3 AND r.state IN ('queued','running','awaiting_approval','awaiting_device') AND c.state='attached' AND c.expires_at>NOW()
			ORDER BY c.created_at DESC`, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentDeviceGrant
			if err := rows.Scan(&item.ID, &item.UserID, &item.AgentID, &item.SpaceID, &item.DeviceID, &item.ScopeID, &item.Capabilities, &item.Metadata, &item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AgentRunDeviceGrants(ctx context.Context, userID, runID string) ([]AgentDeviceGrant, error) {
	items := []AgentDeviceGrant{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.owner_user_id,r.agent_id,c.space_id,c.device_id,c.opaque_ref,c.capabilities,c.metadata,c.expires_at,
			CASE WHEN c.state='attached' THEN NULL ELSE c.updated_at END,c.created_at,c.updated_at FROM agent_run_contexts c JOIN space_runs r ON r.id=c.run_id
			WHERE c.run_id=$1 AND c.owner_user_id=$2 AND r.owner_user_id=$2 AND c.state='attached' AND c.expires_at>NOW()
			ORDER BY c.created_at`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentDeviceGrant
			if err := rows.Scan(&item.ID, &item.UserID, &item.AgentID, &item.SpaceID, &item.DeviceID, &item.ScopeID, &item.Capabilities, &item.Metadata, &item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) GrantAgentDeviceAccess(context.Context, string, string, string, string, string, json.RawMessage, json.RawMessage, time.Time) (*AgentDeviceGrant, error) {
	return nil, ErrSpaceInvalid
}
func (db *Database) RevokeAgentDeviceAccess(context.Context, string, string, string, string) error {
	return ErrSpaceInvalid
}

type AgentRunContext struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	OwnerUserID  string          `json:"owner_user_id"`
	SpaceID      string          `json:"space_id"`
	DeviceID     string          `json:"device_id"`
	Kind         string          `json:"kind"`
	OpaqueRef    string          `json:"opaque_ref"`
	DisplayName  string          `json:"display_name"`
	Capabilities json.RawMessage `json:"capabilities"`
	Metadata     json.RawMessage `json:"metadata"`
	State        string          `json:"state"`
	ExpiresAt    time.Time       `json:"expires_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type AgentDeviceWait struct {
	RunID     string `json:"run_id"`
	HookToken string `json:"-"`
	Available bool   `json:"available"`
}

func (db *Database) AwaitAgentRunDevice(ctx context.Context, runID, runtimeRunID, hookToken string) error {
	if strings.TrimSpace(hookToken) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_device',runtime_phase='awaiting_device',device_wait_hook_token=$3,
			device_wait_expires_at=NOW()+INTERVAL '24 hours',updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND state='running'`, runID, runtimeRunID, hookToken)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err == nil && changed != 1 {
			return ErrSpaceConflict
		}
		return err
	})
}

func (db *Database) AgentDeviceWaitsReady(ctx context.Context, limit int) ([]AgentDeviceWait, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AgentDeviceWait{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.device_wait_hook_token,
			EXISTS(SELECT 1 FROM agent_run_contexts c JOIN trusted_devices d ON d.id=c.device_id
				WHERE c.run_id=r.id AND c.state='attached' AND c.expires_at>NOW() AND d.user_id=r.owner_user_id
				AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds')
			FROM space_runs r WHERE r.state='awaiting_device' AND r.device_wait_hook_token<>''
			AND (r.device_wait_expires_at<=NOW() OR EXISTS(SELECT 1 FROM agent_run_contexts c JOIN trusted_devices d ON d.id=c.device_id
				WHERE c.run_id=r.id AND c.state='attached' AND c.expires_at>NOW() AND d.user_id=r.owner_user_id
				AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds'))
			ORDER BY r.updated_at,r.id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentDeviceWait
			if err := rows.Scan(&item.RunID, &item.HookToken, &item.Available); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) BeginAgentDeviceResume(ctx context.Context, runID string, available bool) error {
	phase := "working"
	if !available {
		phase = "device_unavailable"
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='running',runtime_phase=$2,updated_at=NOW() WHERE id=$1 AND state='awaiting_device'`, runID, phase)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err == nil && changed != 1 {
			return ErrSpaceConflict
		}
		return err
	})
}

func (db *Database) FinishAgentDeviceResume(ctx context.Context, runID string, succeeded bool) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if succeeded {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET device_wait_hook_token='',device_wait_expires_at=NULL,updated_at=NOW() WHERE id=$1 AND state='running'`, runID)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_device',runtime_phase='awaiting_device',updated_at=NOW() WHERE id=$1 AND state='running' AND device_wait_hook_token<>''`, runID)
		return err
	})
}

func (db *Database) AttachAgentRunContext(ctx context.Context, ownerUserID, runID, deviceID, kind, opaqueRef, displayName string, capabilities, metadata json.RawMessage) (*AgentRunContext, error) {
	kind = strings.TrimSpace(kind)
	opaqueRef = strings.TrimSpace(opaqueRef)
	displayName = strings.TrimSpace(displayName)
	capabilities, err := normalizeDeviceAgentCapabilities(capabilities)
	if err != nil || (kind != "browser_tab" && kind != "project_root") || opaqueRef == "" || len(opaqueRef) > 512 {
		return nil, ErrSpaceInvalid
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var object map[string]any
	if json.Unmarshal(metadata, &object) != nil {
		return nil, ErrSpaceInvalid
	}
	out := &AgentRunContext{ID: "context_" + uuid.NewString(), RunID: runID, OwnerUserID: ownerUserID, DeviceID: deviceID, Kind: kind, OpaqueRef: opaqueRef, DisplayName: displayName, Capabilities: capabilities, Metadata: metadata}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT r.space_id FROM space_runs r JOIN personal_agents a ON a.id=r.agent_id WHERE r.id=$1 AND r.owner_user_id=$2 AND a.owner_user_id=$2 AND r.state='queued'`, runID, ownerUserID).Scan(&out.SpaceID); err != nil {
			return ErrSpaceForbidden
		}
		var online bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, ownerUserID).Scan(&online); err != nil || !online {
			return ErrDeviceNotFound
		}
		return tx.QueryRowContext(ctx, `INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW()+INTERVAL '24 hours') RETURNING state,expires_at,created_at,updated_at`, out.ID, out.RunID, out.OwnerUserID, out.SpaceID, out.DeviceID, out.Kind, out.OpaqueRef, out.DisplayName, out.Capabilities, out.Metadata).Scan(&out.State, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	})
	return out, err
}
