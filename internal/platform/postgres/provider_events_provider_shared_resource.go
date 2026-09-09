package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
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
