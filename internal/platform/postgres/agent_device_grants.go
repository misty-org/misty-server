package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
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
	"browser.workspace.visual": true, "browser.workspace.interact": true,
	"files.read": true, "files.write": true, "files.list": true, "files.search": true, "files.copy": true, "files.move": true, "files.delete": true,
	"files.send": true, "files.receive": true,
	"project.patch": true, "project.diff": true, "project.status": true, "project.checks": true, "git.commit": true, "git.push": true, "terminal.execute": true,
	"browser.interact": true, "browser.inspect": true, "browser.visual": true, "browser.navigate": true, "browser.click": true, "browser.type": true, "browser.select": true, "browser.scroll": true,
	"browser.downloads.list": true, "browser.upload": true, "browser.act": true, "browser.confirm_high_risk": true,
	"tabs.list": true, "tabs.open": true, "bookmarks.list": true, "bookmarks.add": true,
}

// deviceContextCapabilities are what each kind of desktop grant may allow.
var deviceContextCapabilities = map[string]map[string]bool{
	"local_folder": {"files.list": true, "files.read": true, "files.send": true},
	// A chat's own device accepts files another of the person's devices sends.
	"inbox": {"files.receive": true},
	"workspace":    {"tabs.list": true, "tabs.open": true, "bookmarks.list": true, "bookmarks.add": true},
}

// DeviceContextCapabilitiesAllowed reports whether a desktop grant asks only
// for capabilities its kind can have.
func DeviceContextCapabilitiesAllowed(kind string, raw json.RawMessage) bool {
	if kind == "browser_tab" {
		return browserOnlyAgentCapabilities(raw)
	}
	allowed := deviceContextCapabilities[kind]
	var capabilities []string
	if allowed == nil || json.Unmarshal(raw, &capabilities) != nil || len(capabilities) == 0 {
		return false
	}
	for _, capability := range capabilities {
		if !allowed[capability] {
			return false
		}
	}
	return true
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

func (db *Database) AgentRunDeviceGrants(ctx context.Context, userID, runID string) ([]AgentDeviceGrant, error) {
	items := []AgentDeviceGrant{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.owner_user_id,r.agent_id,COALESCE(c.space_id,''),c.device_id,c.opaque_ref,c.capabilities,c.metadata,c.expires_at,
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

type AgentDeviceWait struct {
	RunID     string `json:"run_id"`
	HookToken string `json:"-"`
	Available bool   `json:"available"`
}

func (db *Database) AwaitAgentRunDeviceTarget(ctx context.Context, runID, runtimeRunID, hookToken, scopeID, capability string) error {
	if strings.TrimSpace(hookToken) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_device',runtime_phase='awaiting_device',device_wait_hook_token=$3,
			device_wait_expires_at=NOW()+INTERVAL '24 hours',device_wait_scope_id=$4,device_wait_capability=$5,updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND state='running'`, runID, runtimeRunID, hookToken, scopeID, capability)
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
                AND r.device_wait_scope_id<>'' AND c.opaque_ref=r.device_wait_scope_id
                AND r.device_wait_capability<>'' AND c.capabilities ? r.device_wait_capability
				AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds')
			FROM space_runs r WHERE r.state='awaiting_device' AND r.device_wait_hook_token<>''
			AND (r.device_wait_expires_at<=NOW() OR EXISTS(SELECT 1 FROM agent_run_contexts c JOIN trusted_devices d ON d.id=c.device_id
				WHERE c.run_id=r.id AND c.state='attached' AND c.expires_at>NOW() AND d.user_id=r.owner_user_id
                AND r.device_wait_scope_id<>'' AND c.opaque_ref=r.device_wait_scope_id
                AND r.device_wait_capability<>'' AND c.capabilities ? r.device_wait_capability
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
