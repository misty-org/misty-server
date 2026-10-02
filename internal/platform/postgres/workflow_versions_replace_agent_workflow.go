package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) SpaceIntegrations(ctx context.Context, userID, spaceID string) ([]SpaceIntegration, error) {
	items := []SpaceIntegration{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,provider,display_name,'',granted_permissions,status,connected_by_user_id,created_at,updated_at
			FROM space_integrations WHERE space_id=$1 ORDER BY provider,display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceIntegration
			var permissionsRaw []byte
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.Provider, &item.DisplayName, &item.CredentialReference, &permissionsRaw, &item.Status, &item.ConnectedByUserID, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			_ = json.Unmarshal(permissionsRaw, &item.GrantedPermissions)
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SaveSpaceIntegration(ctx context.Context, userID string, item SpaceIntegration) (*SpaceIntegration, error) {
	item.Provider, item.DisplayName, item.CredentialReference = strings.TrimSpace(item.Provider), strings.TrimSpace(item.DisplayName), strings.TrimSpace(item.CredentialReference)
	if !validWorkflowToken(item.Provider, 120) || item.DisplayName == "" || len([]rune(item.DisplayName)) > 120 || item.CredentialReference == "" || len(item.CredentialReference) > 500 {
		return nil, ErrSpaceInvalid
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.GrantedPermissions == nil {
		item.GrantedPermissions = []string{}
	}
	if item.Status != "active" && item.Status != "needs_attention" && item.Status != "disabled" {
		return nil, ErrSpaceInvalid
	}
	for _, permission := range item.GrantedPermissions {
		if !validWorkflowToken(permission, 120) {
			return nil, ErrSpaceInvalid
		}
	}
	if item.ID == "" {
		item.ID = "integration_" + uuid.NewString()
	}
	permissions := mustJSON(item.GrantedPermissions)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionAskRun); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT(id) DO UPDATE SET display_name=EXCLUDED.display_name,credential_reference=EXCLUDED.credential_reference,granted_permissions=EXCLUDED.granted_permissions,status=EXCLUDED.status,updated_at=NOW()
			WHERE space_integrations.space_id=EXCLUDED.space_id AND space_integrations.connected_by_user_id=EXCLUDED.connected_by_user_id
			RETURNING connected_by_user_id,created_at,updated_at`, item.ID, item.SpaceID, item.Provider, item.DisplayName, item.CredentialReference, permissions, item.Status, userID).Scan(&item.ConnectedByUserID, &item.CreatedAt, &item.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	item.CredentialReference = ""
	return &item, nil
}

func loadWorkflowVersionTx(ctx context.Context, tx *sql.Tx, versionID string) (*WorkflowVersion, error) {
	if versionID == "" {
		return nil, ErrSpaceNotFound
	}
	out := &WorkflowVersion{}
	if err := scanWorkflowVersion(tx.QueryRowContext(ctx, `SELECT `+workflowVersionColumns+` FROM space_workflow_versions v WHERE v.id=$1`, versionID), out); err != nil {
		return nil, err
	}
	if !TestingWorkflowChecksumValid(out) {
		return nil, ErrSpaceInvalid
	}
	return out, nil
}

func TestingWorkflowChecksumValid(version *WorkflowVersion) bool {
	if version == nil {
		return false
	}
	metadataRaw, err := json.Marshal(version.Metadata)
	if err != nil {
		return false
	}
	var definition any
	if json.Unmarshal(version.Definition, &definition) != nil {
		return false
	}
	definitionRaw, err := json.Marshal(definition)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(append(append([]byte{}, metadataRaw...), definitionRaw...))
	return hex.EncodeToString(digest[:]) == version.ChecksumSHA256
}

func loadLatestWorkflowVersionTx(ctx context.Context, tx *sql.Tx, workflowID string) (*WorkflowVersion, error) {
	var versionID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM space_workflow_versions WHERE workflow_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, workflowID).Scan(&versionID); err != nil {
		return nil, err
	}
	return loadWorkflowVersionTx(ctx, tx, versionID)
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
