package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

type FigmaBinding struct {
	ID               string     `json:"id"`
	SpaceID          string     `json:"space_id"`
	ConnectionID     string     `json:"connection_id"`
	IntegrationID    string     `json:"integration_id"`
	SharedResourceID string     `json:"shared_resource_id"`
	BoundByUserID    string     `json:"bound_by_user_id"`
	ResourceType     string     `json:"resource_type"`
	ExternalID       string     `json:"external_id"`
	DisplayName      string     `json:"display_name"`
	TeamID           string     `json:"team_id,omitempty"`
	ProjectID        string     `json:"project_id,omitempty"`
	FileKey          string     `json:"file_key,omitempty"`
	SyncCursor       string     `json:"sync_cursor,omitempty"`
	Status           string     `json:"status"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const figmaBindingColumns = `id,space_id,connection_id,integration_id,shared_resource_id,bound_by_user_id,resource_type,external_id,display_name,team_id,project_id,file_key,sync_cursor,status,last_error_code,last_synced_at,created_at,updated_at`

func scanFigmaBinding(row interface{ Scan(...any) error }, item *FigmaBinding) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.ConnectionID, &item.IntegrationID, &item.SharedResourceID, &item.BoundByUserID, &item.ResourceType, &item.ExternalID, &item.DisplayName, &item.TeamID, &item.ProjectID, &item.FileKey, &item.SyncCursor, &item.Status, &item.LastErrorCode, &item.LastSyncedAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) CreateFigmaBinding(ctx context.Context, userID, spaceID, connectionID string, item FigmaBinding) (*FigmaBinding, error) {
	item.ResourceType = strings.TrimSpace(item.ResourceType)
	item.ExternalID = strings.TrimSpace(item.ExternalID)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	if !oneOf(item.ResourceType, "file", "project") || item.ExternalID == "" || item.DisplayName == "" {
		return nil, ErrSpaceInvalid
	}
	if item.ResourceType == "file" {
		item.FileKey = item.ExternalID
		item.ProjectID = ""
	} else {
		item.ProjectID = item.ExternalID
		item.FileKey = ""
	}
	out := &FigmaBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return ErrSpaceForbidden
		}
		var accountDisplay string
		var capabilities []byte
		if err := tx.QueryRowContext(ctx, `SELECT account_display,capabilities FROM connected_accounts WHERE id=$1 AND user_id=$2 AND provider='figma' AND status='active' AND revoked_at IS NULL`, connectionID, userID).Scan(&accountDisplay, &capabilities); err != nil {
			return ErrSpaceForbidden
		}
		var values []string
		_ = json.Unmarshal(capabilities, &values)
		if !containsDBString(values, "drawings_read") {
			return ErrSpaceForbidden
		}
		integrationID := "integration_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id) VALUES($1,$2,'figma',$3,$4,'[]'::jsonb,'active',$5) ON CONFLICT(space_id,connected_by_user_id,provider,display_name) DO UPDATE SET credential_reference=EXCLUDED.credential_reference,status='active',updated_at=NOW()`, integrationID, spaceID, accountDisplay, "connected-account:"+connectionID, userID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM space_integrations WHERE space_id=$1 AND connected_by_user_id=$2 AND provider='figma' AND display_name=$3`, spaceID, userID, accountDisplay).Scan(&integrationID); err != nil {
			return err
		}
		resourceID := "provider_resource_" + uuid.NewString()
		configuration := mustJSON(map[string]any{"connection_id": connectionID, "resource_type": item.ResourceType, "team_id": item.TeamID, "project_id": item.ProjectID, "file_key": item.FileKey})
		if err := tx.QueryRowContext(ctx, `INSERT INTO provider_shared_resources(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope,configuration) VALUES($1,$2,$3,$4,'figma',$5,$6,$7,$8,$9) ON CONFLICT(space_id,integration_id,provider,resource_type,external_resource_id) DO UPDATE SET display_name=EXCLUDED.display_name,configuration=EXCLUDED.configuration,status='active',last_error_code='',updated_at=NOW() RETURNING id`, resourceID, spaceID, integrationID, userID, item.ResourceType, item.ExternalID, item.DisplayName, "figma:"+item.ResourceType+":"+item.ExternalID, configuration).Scan(&resourceID); err != nil {
			return err
		}
		return scanFigmaBinding(tx.QueryRowContext(ctx, `INSERT INTO figma_space_bindings(id,space_id,connection_id,integration_id,shared_resource_id,bound_by_user_id,resource_type,external_id,display_name,team_id,project_id,file_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(space_id,resource_type,external_id) DO UPDATE SET connection_id=EXCLUDED.connection_id,integration_id=EXCLUDED.integration_id,shared_resource_id=EXCLUDED.shared_resource_id,bound_by_user_id=EXCLUDED.bound_by_user_id,display_name=EXCLUDED.display_name,team_id=EXCLUDED.team_id,project_id=EXCLUDED.project_id,file_key=EXCLUDED.file_key,status='pending',last_error_code='',disabled_at=NULL,updated_at=NOW() RETURNING `+figmaBindingColumns, "figma_binding_"+uuid.NewString(), spaceID, connectionID, integrationID, resourceID, userID, item.ResourceType, item.ExternalID, item.DisplayName, item.TeamID, item.ProjectID, item.FileKey), out)
	})
	return out, err
}

func (db *Database) FigmaBindings(ctx context.Context, userID, spaceID string) ([]FigmaBinding, error) {
	items := []FigmaBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+figmaBindingColumns+` FROM figma_space_bindings WHERE space_id=$1 AND disabled_at IS NULL ORDER BY display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item FigmaBinding
			if err := scanFigmaBinding(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
func (db *Database) FigmaBinding(ctx context.Context, userID, spaceID, id string) (*FigmaBinding, error) {
	item := &FigmaBinding{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanFigmaBinding(tx.QueryRowContext(ctx, `SELECT `+figmaBindingColumns+` FROM figma_space_bindings WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, id, spaceID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return item, err
}
func (db *Database) FigmaBindingByWebhookID(ctx context.Context, webhookID string) (*FigmaBinding, string, string, string, error) {
	item := &FigmaBinding{}
	var subscriptionID, passcodeHash, eventType string
	columns := figmaPrefixedColumns("b", figmaBindingColumns)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanFigmaBindingWithSubscription(tx.QueryRowContext(ctx, `SELECT `+columns+`,s.id,s.passcode_hash,s.event_type FROM figma_space_bindings b JOIN figma_webhook_subscriptions s ON s.binding_id=b.id WHERE s.webhook_id=$1 AND s.status='active' AND b.disabled_at IS NULL`, webhookID), item, &subscriptionID, &passcodeHash, &eventType)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", "", ErrSpaceNotFound
	}
	return item, subscriptionID, passcodeHash, eventType, err
}
func scanFigmaBindingWithSubscription(row interface{ Scan(...any) error }, item *FigmaBinding, subscriptionID, passcodeHash, eventType *string) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.ConnectionID, &item.IntegrationID, &item.SharedResourceID, &item.BoundByUserID, &item.ResourceType, &item.ExternalID, &item.DisplayName, &item.TeamID, &item.ProjectID, &item.FileKey, &item.SyncCursor, &item.Status, &item.LastErrorCode, &item.LastSyncedAt, &item.CreatedAt, &item.UpdatedAt, subscriptionID, passcodeHash, eventType)
}
func figmaPrefixedColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i := range parts {
		parts[i] = alias + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}
func containsDBString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
