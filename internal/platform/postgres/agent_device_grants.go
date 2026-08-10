package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AgentDeviceGrant struct {
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	AgentID      string          `json:"agent_id"`
	SpaceID      string          `json:"space_id"`
	DeviceID     string          `json:"device_id"`
	ScopeID      string          `json:"scope_id"`
	Capabilities json.RawMessage `json:"capabilities"`
	ExpiresAt    time.Time       `json:"expires_at"`
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

var deviceAgentCapabilities = map[string]bool{
	"files.read": true, "files.write": true, "resources.delete": true,
	"files.list": true, "files.search": true, "files.preview": true,
	"files.organize": true, "files.copy": true, "files.move": true,
	"files.rename": true, "files.delete": true,
	"transfers.inspect": true, "transfers.create": true, "transfers.pause": true,
	"transfers.resume": true, "transfers.retry": true, "transfers.cancel": true,
}

func normalizeDeviceAgentCapabilities(raw json.RawMessage) (json.RawMessage, error) {
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !deviceAgentCapabilities[value] {
			return nil, ErrSpaceInvalid
		}
		if !seen[value] {
			seen[value] = true
			normalized = append(normalized, value)
		}
	}
	return json.Marshal(normalized)
}

const agentDeviceGrantColumns = `id,user_id,agent_id,space_id,device_id,scope_id,capabilities,expires_at,revoked_at,created_at,updated_at`

func scanAgentDeviceGrant(row scanner, item *AgentDeviceGrant) error {
	return row.Scan(&item.ID, &item.UserID, &item.AgentID, &item.SpaceID, &item.DeviceID, &item.ScopeID, &item.Capabilities, &item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) AgentDeviceGrants(ctx context.Context, userID, spaceID, agentID string) ([]AgentDeviceGrant, error) {
	items := []AgentDeviceGrant{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+agentDeviceGrantColumns+` FROM agent_device_grants WHERE user_id=$1 AND space_id=$2 AND agent_id=$3 ORDER BY created_at DESC`, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentDeviceGrant
			if err := scanAgentDeviceGrant(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) GrantAgentDeviceAccess(ctx context.Context, userID, spaceID, agentID, deviceID, scopeID string, capabilities json.RawMessage, expiresAt time.Time) (*AgentDeviceGrant, error) {
	scopeID = strings.TrimSpace(scopeID)
	capabilities, err := normalizeDeviceAgentCapabilities(capabilities)
	if err != nil || scopeID == "" || len(scopeID) > 256 || !expiresAt.After(time.Now()) || expiresAt.After(time.Now().Add(31*24*time.Hour)) {
		return nil, ErrSpaceInvalid
	}
	item := &AgentDeviceGrant{}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		var online bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND last_seen_at>NOW()-INTERVAL '90 seconds')`, deviceID, userID).Scan(&online); err != nil || !online {
			return ErrDeviceNotFound
		}
		return scanAgentDeviceGrant(tx.QueryRowContext(ctx, `INSERT INTO agent_device_grants(id,user_id,agent_id,space_id,device_id,scope_id,capabilities,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(user_id,agent_id,space_id,device_id,scope_id) DO UPDATE SET capabilities=EXCLUDED.capabilities,expires_at=EXCLUDED.expires_at,revoked_at=NULL,updated_at=NOW()
			RETURNING `+agentDeviceGrantColumns, "devicegrant_"+uuid.NewString(), userID, agentID, spaceID, deviceID, scopeID, capabilities, expiresAt), item)
	})
	return item, err
}

func (db *Database) RevokeAgentDeviceAccess(ctx context.Context, userID, spaceID, agentID, grantID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var deviceID string
		err := tx.QueryRowContext(ctx, `UPDATE agent_device_grants SET revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND space_id=$3 AND agent_id=$4 RETURNING device_id`, grantID, userID, spaceID, agentID).Scan(&deviceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE workflow_device_node_jobs SET state='canceled',completed_at=NOW(),lease_expires_at=NULL
			WHERE device_grant_id=$1 AND state IN ('queued','leased')`, grantID)
		return err
	})
}
