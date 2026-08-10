package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) FinishProviderEvent(ctx context.Context, integrationID, externalEventID, state string) error {
	if state != "processed" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE provider_event_inbox SET state=$1,processed_at=NOW() WHERE integration_id=$2 AND external_event_id=$3`, state, integrationID, externalEventID)
		return err
	})
}

func (db *Database) UpsertProviderContentRecord(ctx context.Context, item ProviderContentRecord) error {
	if item.ID == "" {
		item.ID = "provider_record_" + uuid.NewString()
	}
	if item.MIMEType == "" {
		item.MIMEType = "application/json"
	}
	if len(item.Content) == 0 {
		item.Content = json.RawMessage(`{}`)
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_content_records(id,space_id,shared_resource_id,provider,external_record_id,parent_external_id,record_type,fingerprint,display_name,mime_type,occurred_at,content,deleted_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(shared_resource_id,external_record_id) DO UPDATE SET parent_external_id=EXCLUDED.parent_external_id,record_type=EXCLUDED.record_type,fingerprint=EXCLUDED.fingerprint,display_name=EXCLUDED.display_name,mime_type=EXCLUDED.mime_type,occurred_at=EXCLUDED.occurred_at,content=EXCLUDED.content,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`, item.ID, item.SpaceID, item.SharedResourceID, item.Provider, item.ExternalRecordID, item.ParentExternalID, item.RecordType, item.Fingerprint, item.DisplayName, item.MIMEType, item.OccurredAt, item.Content, item.DeletedAt)
		return err
	})
}

func (db *Database) ProviderContentRecords(ctx context.Context, userID, spaceID, provider, query string, limit int) ([]ProviderContentRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	out := []ProviderContentRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,shared_resource_id,provider,external_record_id,parent_external_id,record_type,fingerprint,display_name,mime_type,occurred_at,content,deleted_at,created_at,updated_at
			FROM provider_content_records WHERE space_id=$1 AND provider=$2 AND deleted_at IS NULL AND ($3='' OR display_name ILIKE '%%'||$3||'%%' OR content::text ILIKE '%%'||$3||'%%') ORDER BY occurred_at DESC NULLS LAST,updated_at DESC LIMIT $4`, spaceID, provider, query, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ProviderContentRecord
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SharedResourceID, &item.Provider, &item.ExternalRecordID, &item.ParentExternalID, &item.RecordType, &item.Fingerprint, &item.DisplayName, &item.MIMEType, &item.OccurredAt, &item.Content, &item.DeletedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) ProviderContentRecord(ctx context.Context, userID, spaceID, provider, externalRecordID string) (*ProviderContentRecord, error) {
	out := &ProviderContentRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT id,space_id,shared_resource_id,provider,external_record_id,parent_external_id,record_type,fingerprint,display_name,mime_type,occurred_at,content,deleted_at,created_at,updated_at FROM provider_content_records WHERE space_id=$1 AND provider=$2 AND external_record_id=$3 AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`, spaceID, provider, externalRecordID).Scan(&out.ID, &out.SpaceID, &out.SharedResourceID, &out.Provider, &out.ExternalRecordID, &out.ParentExternalID, &out.RecordType, &out.Fingerprint, &out.DisplayName, &out.MIMEType, &out.OccurredAt, &out.Content, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ProviderSharedResourceForDestination resolves an outbound destination only
// when it was explicitly published to the Space. The returned integration is
// still owned by the installer; callers never receive its credential.
func (db *Database) ProviderSharedResourceForDestination(ctx context.Context, userID, spaceID, provider, externalResourceID string) (*ProviderSharedResource, error) {
	out := &ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanSharedResource(tx.QueryRowContext(ctx, `SELECT `+sharedResourceColumns+` FROM provider_shared_resources
			WHERE space_id=$1 AND provider=$2 AND external_resource_id=$3 AND resource_type='channel' AND status='active'
			ORDER BY updated_at DESC LIMIT 1`, spaceID, provider, externalResourceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ProviderSharedResourceForNotionEntity resolves either a selected Notion
// source or an entity already discovered beneath that source. It never falls
// back to an unselected workspace object.
func (db *Database) ProviderSharedResourceForNotionEntity(
	ctx context.Context,
	userID, spaceID, externalResourceID string,
) (*ProviderSharedResource, error) {
	out := &ProviderSharedResource{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanSharedResource(tx.QueryRowContext(ctx, `SELECT `+prefixedSharedResourceColumns("r")+`
			FROM provider_shared_resources r
			LEFT JOIN provider_content_records c
			  ON c.shared_resource_id=r.id AND c.deleted_at IS NULL
			WHERE r.space_id=$1 AND r.provider='notion' AND r.status='active'
			  AND (r.external_resource_id=$2 OR c.external_record_id=$2)
			ORDER BY CASE WHEN r.external_resource_id=$2 THEN 0 ELSE 1 END,r.updated_at DESC
			LIMIT 1`, spaceID, externalResourceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func prefixedSharedResourceColumns(alias string) string {
	parts := strings.Split(sharedResourceColumns, ",")
	for index := range parts {
		parts[index] = alias + "." + parts[index]
	}
	return strings.Join(parts, ",")
}
