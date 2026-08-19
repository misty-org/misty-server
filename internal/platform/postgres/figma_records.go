package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"time"
)

type FigmaContentRecord struct {
	ID               string          `json:"id"`
	SpaceID          string          `json:"space_id"`
	BindingID        string          `json:"binding_id"`
	FileKey          string          `json:"file_key"`
	RecordType       string          `json:"record_type"`
	ExternalID       string          `json:"external_id"`
	ParentExternalID string          `json:"parent_external_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	ActorID          string          `json:"actor_id,omitempty"`
	ActorName        string          `json:"actor_name,omitempty"`
	Resolved         *bool           `json:"resolved,omitempty"`
	Fingerprint      string          `json:"fingerprint"`
	Provenance       json.RawMessage `json:"provenance"`
	OccurredAt       *time.Time      `json:"occurred_at,omitempty"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
}

func (db *Database) UpsertFigmaContentRecord(ctx context.Context, item FigmaContentRecord) error {
	if !oneOf(item.RecordType, "file", "version", "comment", "webhook_event") || item.BindingID == "" || item.ExternalID == "" || len(item.Fingerprint) != 64 {
		return ErrSpaceInvalid
	}
	if item.ID == "" {
		item.ID = "figma_record_" + uuid.NewString()
	}
	if len(item.Provenance) == 0 {
		item.Provenance = json.RawMessage(`{}`)
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, resourceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id,shared_resource_id FROM figma_space_bindings WHERE id=$1 AND disabled_at IS NULL`, item.BindingID).Scan(&spaceID, &resourceID); err != nil {
			return err
		}
		item.SpaceID = spaceID
		_, err := tx.ExecContext(ctx, `INSERT INTO figma_content_records(id,space_id,binding_id,file_key,record_type,external_id,parent_external_id,title,actor_id,actor_name,resolved,fingerprint,provenance,occurred_at,deleted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(binding_id,record_type,external_id) DO UPDATE SET file_key=EXCLUDED.file_key,parent_external_id=EXCLUDED.parent_external_id,title=EXCLUDED.title,actor_id=EXCLUDED.actor_id,actor_name=EXCLUDED.actor_name,resolved=EXCLUDED.resolved,fingerprint=EXCLUDED.fingerprint,provenance=EXCLUDED.provenance,occurred_at=EXCLUDED.occurred_at,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`, item.ID, item.SpaceID, item.BindingID, item.FileKey, item.RecordType, item.ExternalID, item.ParentExternalID, item.Title, item.ActorID, item.ActorName, item.Resolved, item.Fingerprint, item.Provenance, item.OccurredAt, item.DeletedAt)
		if err != nil {
			return err
		}
		content := mustJSON(map[string]any{"file_key": item.FileKey, "record_type": item.RecordType, "external_id": item.ExternalID, "parent_external_id": item.ParentExternalID, "title": item.Title, "actor_id": item.ActorID, "actor_name": item.ActorName, "resolved": item.Resolved, "provenance": json.RawMessage(item.Provenance)})
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_content_records(id,space_id,shared_resource_id,provider,external_record_id,parent_external_id,record_type,fingerprint,display_name,mime_type,occurred_at,content,deleted_at) VALUES($1,$2,$3,'figma',$4,$5,$6,$7,$8,'application/vnd.figma+json',$9,$10,$11) ON CONFLICT(shared_resource_id,external_record_id) DO UPDATE SET parent_external_id=EXCLUDED.parent_external_id,record_type=EXCLUDED.record_type,fingerprint=EXCLUDED.fingerprint,display_name=EXCLUDED.display_name,occurred_at=EXCLUDED.occurred_at,content=EXCLUDED.content,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`, `provider_record_`+uuid.NewString(), item.SpaceID, resourceID, item.RecordType+":"+item.ExternalID, item.ParentExternalID, item.RecordType, item.Fingerprint, firstNonBlank(item.Title, item.ExternalID), item.OccurredAt, content, item.DeletedAt)
		return err
	})
}
func (db *Database) FigmaContentRecords(ctx context.Context, userID, spaceID, bindingID, recordType, query string, limit int) ([]FigmaContentRecord, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []FigmaContentRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,binding_id,file_key,record_type,external_id,parent_external_id,title,actor_id,actor_name,resolved,fingerprint,provenance,occurred_at,deleted_at FROM figma_content_records WHERE space_id=$1 AND binding_id=$2 AND deleted_at IS NULL AND ($3='' OR record_type=$3) AND ($4='' OR title ILIKE '%%'||$4||'%%' OR actor_name ILIKE '%%'||$4||'%%') ORDER BY occurred_at DESC NULLS LAST,updated_at DESC LIMIT $5`, spaceID, bindingID, recordType, strings.TrimSpace(query), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item FigmaContentRecord
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.BindingID, &item.FileKey, &item.RecordType, &item.ExternalID, &item.ParentExternalID, &item.Title, &item.ActorID, &item.ActorName, &item.Resolved, &item.Fingerprint, &item.Provenance, &item.OccurredAt, &item.DeletedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) FigmaBindingContainsFile(ctx context.Context, binding *FigmaBinding, fileKey string) (bool, error) {
	if binding == nil || fileKey == "" {
		return false, ErrSpaceInvalid
	}
	if binding.ResourceType == "file" {
		return binding.FileKey == fileKey, nil
	}
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM figma_content_records WHERE binding_id=$1 AND record_type='file' AND file_key=$2 AND deleted_at IS NULL)`, binding.ID, fileKey).Scan(&allowed)
	})
	return allowed, err
}

func (db *Database) MarkFigmaFileDeleted(ctx context.Context, binding *FigmaBinding, fileKey string) error {
	if binding == nil || fileKey == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE figma_content_records SET deleted_at=NOW(),updated_at=NOW() WHERE binding_id=$1 AND file_key=$2 AND deleted_at IS NULL`, binding.ID, fileKey); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_content_records SET deleted_at=NOW(),updated_at=NOW() WHERE shared_resource_id=$1 AND content->>'file_key'=$2 AND deleted_at IS NULL`, binding.SharedResourceID, fileKey); err != nil {
			return err
		}
		if binding.ResourceType == "file" {
			_, err := tx.ExecContext(ctx, `UPDATE figma_space_bindings SET status='needs_attention',last_error_code='figma_file_deleted',updated_at=NOW() WHERE id=$1`, binding.ID)
			return err
		}
		return nil
	})
}
