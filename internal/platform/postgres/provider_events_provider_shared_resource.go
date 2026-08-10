package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ProviderSharedResource struct {
	ID                 string          `json:"id"`
	SpaceID            string          `json:"space_id"`
	IntegrationID      string          `json:"integration_id"`
	PublishedByUserID  string          `json:"published_by_user_id"`
	Provider           string          `json:"provider"`
	ResourceType       string          `json:"resource_type"`
	ExternalResourceID string          `json:"external_resource_id"`
	DisplayName        string          `json:"display_name"`
	PermissionScope    string          `json:"permission_scope"`
	Configuration      json.RawMessage `json:"configuration"`
	Status             string          `json:"status"`
	LastErrorCode      string          `json:"last_error_code,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type ProviderContentRecord struct {
	ID               string          `json:"id"`
	SpaceID          string          `json:"space_id"`
	SharedResourceID string          `json:"shared_resource_id"`
	Provider         string          `json:"provider"`
	ExternalRecordID string          `json:"external_record_id"`
	ParentExternalID string          `json:"parent_external_id,omitempty"`
	RecordType       string          `json:"record_type"`
	Fingerprint      string          `json:"fingerprint"`
	DisplayName      string          `json:"display_name"`
	MIMEType         string          `json:"mime_type"`
	OccurredAt       *time.Time      `json:"occurred_at,omitempty"`
	Content          json.RawMessage `json:"content"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

const sharedResourceColumns = `id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope,configuration,status,last_error_code,created_at,updated_at`

func scanSharedResource(row interface{ Scan(...any) error }, out *ProviderSharedResource) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.IntegrationID, &out.PublishedByUserID, &out.Provider, &out.ResourceType, &out.ExternalResourceID, &out.DisplayName, &out.PermissionScope, &out.Configuration, &out.Status, &out.LastErrorCode, &out.CreatedAt, &out.UpdatedAt)
}

func validateSharedResource(item *ProviderSharedResource) error {
	item.Provider = strings.TrimSpace(item.Provider)
	item.ResourceType = strings.TrimSpace(item.ResourceType)
	item.ExternalResourceID = strings.TrimSpace(item.ExternalResourceID)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	if item.SpaceID == "" || item.IntegrationID == "" || item.ExternalResourceID == "" || len([]rune(item.DisplayName)) < 1 || len([]rune(item.DisplayName)) > 240 {
		return ErrSpaceInvalid
	}
	switch item.Provider {
	case "slack", "discord":
		if item.ResourceType != "channel" {
			return ErrSpaceInvalid
		}
	case "notion":
		if item.ResourceType != "page" && item.ResourceType != "database" && item.ResourceType != "data_source" {
			return ErrSpaceInvalid
		}
	default:
		return ErrSpaceInvalid
	}
	if len(item.Configuration) == 0 {
		item.Configuration = json.RawMessage(`{}`)
	}
	var config map[string]any
	if json.Unmarshal(item.Configuration, &config) != nil {
		return ErrSpaceInvalid
	}
	item.PermissionScope = item.Provider + ":" + item.ResourceType + ":" + item.ExternalResourceID
	return nil
}

func (db *Database) ProviderSharedResources(ctx context.Context, userID, spaceID string) ([]ProviderSharedResource, error) {
	out := []ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+sharedResourceColumns+` FROM provider_shared_resources WHERE space_id=$1 ORDER BY provider,display_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ProviderSharedResource
			if err := scanSharedResource(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) ReplaceProviderSharedResources(
	ctx context.Context,
	userID, spaceID, integrationID string,
	items []ProviderSharedResource,
) ([]ProviderSharedResource, error) {
	for index := range items {
		items[index].SpaceID = spaceID
		items[index].IntegrationID = integrationID
		if err := validateSharedResource(&items[index]); err != nil {
			return nil, err
		}
	}
	out := []ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var provider string
		if err := tx.QueryRowContext(ctx, `SELECT provider FROM space_integrations
			WHERE id=$1 AND space_id=$2 AND status='active'`,
			integrationID, spaceID).Scan(&provider); err != nil {
			return ErrSpaceInvalid
		}
		for _, item := range items {
			if item.Provider != provider {
				return ErrSpaceInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_shared_resources
			SET status='disabled',updated_at=NOW()
			WHERE space_id=$1 AND integration_id=$2 AND status<>'disabled'`,
			spaceID, integrationID); err != nil {
			return err
		}
		for _, item := range items {
			id := "provider_resource_" + uuid.NewString()
			query := `INSERT INTO provider_shared_resources
				(id,space_id,integration_id,published_by_user_id,provider,resource_type,
				 external_resource_id,display_name,permission_scope,configuration)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT(space_id,integration_id,provider,resource_type,external_resource_id)
				DO UPDATE SET display_name=EXCLUDED.display_name,
				  permission_scope=EXCLUDED.permission_scope,
				  configuration=EXCLUDED.configuration,status='active',
				  last_error_code='',updated_at=NOW()
				RETURNING ` + sharedResourceColumns
			var stored ProviderSharedResource
			if err := scanSharedResource(tx.QueryRowContext(ctx, query, id, spaceID, integrationID,
				userID, item.Provider, item.ResourceType, item.ExternalResourceID, item.DisplayName,
				item.PermissionScope, item.Configuration), &stored); err != nil {
				return err
			}
			out = append(out, stored)
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "integration.resources_replaced",
			integrationID, map[string]any{"resource_count": len(out)})
		return err
	})
	return out, err
}

func (db *Database) PublishProviderSharedResource(ctx context.Context, userID string, item ProviderSharedResource) (*ProviderSharedResource, error) {
	if err := validateSharedResource(&item); err != nil {
		return nil, err
	}
	item.ID = "provider_resource_" + uuid.NewString()
	out := &ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var provider string
		if err := tx.QueryRowContext(ctx, `SELECT provider FROM space_integrations WHERE id=$1 AND space_id=$2 AND connected_by_user_id=$3 AND status='active'`, item.IntegrationID, item.SpaceID, userID).Scan(&provider); err != nil || provider != item.Provider {
			return ErrSpaceInvalid
		}
		query := `INSERT INTO provider_shared_resources(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope,configuration)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(space_id,integration_id,provider,resource_type,external_resource_id) DO UPDATE SET display_name=EXCLUDED.display_name,permission_scope=EXCLUDED.permission_scope,configuration=EXCLUDED.configuration,status='active',last_error_code='',updated_at=NOW() RETURNING ` + sharedResourceColumns
		if err := scanSharedResource(tx.QueryRowContext(ctx, query, item.ID, item.SpaceID, item.IntegrationID, userID, item.Provider, item.ResourceType, item.ExternalResourceID, item.DisplayName, item.PermissionScope, item.Configuration), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, item.SpaceID, userID, "integration.resource_published", out.ID, map[string]any{"resource": out})
		return err
	})
	return out, err
}

func (db *Database) DisableProviderSharedResource(ctx context.Context, userID, spaceID, resourceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE provider_shared_resources SET status='disabled',updated_at=NOW() WHERE id=$1 AND space_id=$2`, resourceID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "integration.resource_disabled", resourceID, map[string]any{})
		return err
	})
}

// MatchingProviderResources resolves an incoming provider event only to
// explicitly published resources. accountID is the OAuth workspace/guild ID;
// an empty value is allowed for the global Discord bot installation.
func (db *Database) MatchingProviderResources(ctx context.Context, provider, accountID, externalResourceID string) ([]ProviderSharedResource, error) {
	out := []ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+sharedResourceColumns+` FROM provider_shared_resources r
			JOIN space_integrations i ON i.id=r.integration_id JOIN space_provider_credentials c ON c.integration_id=i.id
			WHERE r.provider=$1 AND r.external_resource_id=$2 AND r.status='active' AND i.status='active' AND c.revoked_at IS NULL
			AND ($3='' OR c.account_id=$3 OR r.configuration->>'accountId'=$3)`, provider, externalResourceID, accountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ProviderSharedResource
			if err := scanSharedResource(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) EnqueueProviderEvent(ctx context.Context, resource ProviderSharedResource, externalEventID string, payload json.RawMessage) (bool, error) {
	inserted := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO provider_event_inbox(integration_id,user_id,provider,external_event_id,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(integration_id,external_event_id) DO NOTHING`, resource.IntegrationID, resource.PublishedByUserID, resource.Provider, externalEventID, payload)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		inserted = changed == 1
		return nil
	})
	return inserted, err
}
