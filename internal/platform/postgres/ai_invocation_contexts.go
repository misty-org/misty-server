package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AIInvocationContext struct {
	ID           string          `json:"id"`
	InvocationID string          `json:"invocation_id"`
	UserID       string          `json:"user_id"`
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

func (db *Database) AttachAIInvocationContext(ctx context.Context, userID, invocationID, spaceID, deviceID, kind, opaqueRef, displayName string, capabilities, metadata json.RawMessage) (*AIInvocationContext, error) {
	kind, opaqueRef, displayName = strings.TrimSpace(kind), strings.TrimSpace(opaqueRef), strings.TrimSpace(displayName)
	capabilities, err := normalizeDeviceAgentCapabilities(capabilities)
	if err != nil || kind != "browser_tab" || opaqueRef == "" || len(opaqueRef) > 512 || !browserOnlyAgentCapabilities(capabilities) {
		return nil, ErrSpaceInvalid
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var metadataObject map[string]any
	if json.Unmarshal(metadata, &metadataObject) != nil {
		return nil, ErrSpaceInvalid
	}
	out := &AIInvocationContext{
		ID: "aicontext_" + uuid.NewString(), InvocationID: invocationID, UserID: userID,
		SpaceID: spaceID, DeviceID: deviceID, Kind: kind, OpaqueRef: opaqueRef,
		DisplayName: displayName, Capabilities: capabilities, Metadata: metadata,
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations WHERE id=$1 AND user_id=$2 AND state='queued')`, invocationID, userID).Scan(&valid); err != nil || !valid {
			if err != nil {
				return err
			}
			return ErrSpaceConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, userID).Scan(&valid); err != nil || !valid {
			if err != nil {
				return err
			}
			return ErrDeviceNotFound
		}
		return tx.QueryRowContext(ctx, `INSERT INTO ai_invocation_contexts(id,invocation_id,user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING state,expires_at,created_at,updated_at`, out.ID, out.InvocationID, out.UserID, out.SpaceID, out.DeviceID, out.Kind, out.OpaqueRef, out.DisplayName, out.Capabilities, out.Metadata).
			Scan(&out.State, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	})
	return out, err
}

func (db *Database) AIInvocationContexts(ctx context.Context, userID, invocationID string) ([]AIInvocationContext, error) {
	items := []AIInvocationContext{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.invocation_id,c.user_id,c.space_id,c.device_id,c.kind,c.opaque_ref,c.display_name,c.capabilities,c.metadata,c.state,c.expires_at,c.created_at,c.updated_at
			FROM ai_invocation_contexts c JOIN ai_invocations i ON i.id=c.invocation_id AND i.user_id=$1
			WHERE c.invocation_id=$2 AND c.user_id=$1 AND c.state='attached' AND c.expires_at>NOW() ORDER BY c.created_at`, userID, invocationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIInvocationContext
			if err := rows.Scan(&item.ID, &item.InvocationID, &item.UserID, &item.SpaceID, &item.DeviceID, &item.Kind, &item.OpaqueRef, &item.DisplayName, &item.Capabilities, &item.Metadata, &item.State, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func browserOnlyAgentCapabilities(raw json.RawMessage) bool {
	var capabilities []string
	if json.Unmarshal(raw, &capabilities) != nil || len(capabilities) == 0 {
		return false
	}
	for _, capability := range capabilities {
		if !strings.HasPrefix(capability, "browser.") {
			return false
		}
	}
	return true
}
