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

type GitHubRepositoryRecord struct {
	ID               string          `json:"id"`
	SpaceID          string          `json:"space_id"`
	WorkspaceID      string          `json:"workspace_id"`
	RepositoryID     int64           `json:"repository_id"`
	RecordType       string          `json:"record_type"`
	ExternalID       string          `json:"external_id"`
	ParentExternalID string          `json:"parent_external_id,omitempty"`
	RefName          string          `json:"ref_name,omitempty"`
	SHA              string          `json:"sha,omitempty"`
	Number           *int64          `json:"number,omitempty"`
	State            string          `json:"state,omitempty"`
	Title            string          `json:"title,omitempty"`
	URL              string          `json:"url,omitempty"`
	ActorLogin       string          `json:"actor_login,omitempty"`
	Fingerprint      string          `json:"fingerprint"`
	Provenance       json.RawMessage `json:"provenance"`
	OccurredAt       *time.Time      `json:"occurred_at,omitempty"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
}

func (db *Database) UpsertGitHubRepositoryRecord(ctx context.Context, item GitHubRepositoryRecord) error {
	if !oneOf(item.RecordType, "repository", "branch", "commit", "issue", "pull_request") || item.WorkspaceID == "" || item.ExternalID == "" {
		return ErrSpaceInvalid
	}
	if item.ID == "" {
		item.ID = "ghrecord_" + uuid.NewString()
	}
	if len(item.Provenance) == 0 {
		item.Provenance = json.RawMessage(`{}`)
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, sharedResourceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id,shared_resource_id FROM github_code_workspaces WHERE id=$1 AND disabled_at IS NULL`, item.WorkspaceID).Scan(&spaceID, &sharedResourceID); err != nil {
			return err
		}
		item.SpaceID = spaceID
		_, err := tx.ExecContext(ctx, `INSERT INTO github_repository_records
			(id,space_id,workspace_id,repository_id,record_type,external_id,parent_external_id,ref_name,sha,
			 number,state,title,url,actor_login,fingerprint,provenance,occurred_at,deleted_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT(workspace_id,record_type,external_id) DO UPDATE SET parent_external_id=EXCLUDED.parent_external_id,
			 ref_name=EXCLUDED.ref_name,sha=EXCLUDED.sha,number=EXCLUDED.number,state=EXCLUDED.state,title=EXCLUDED.title,
			 url=EXCLUDED.url,actor_login=EXCLUDED.actor_login,fingerprint=EXCLUDED.fingerprint,
			 provenance=EXCLUDED.provenance,occurred_at=EXCLUDED.occurred_at,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`,
			item.ID, item.SpaceID, item.WorkspaceID, item.RepositoryID, item.RecordType, item.ExternalID,
			item.ParentExternalID, item.RefName, item.SHA, item.Number, item.State, item.Title, item.URL,
			item.ActorLogin, item.Fingerprint, item.Provenance, item.OccurredAt, item.DeletedAt)
		if err != nil {
			return err
		}
		content := mustJSON(map[string]any{"record_type": item.RecordType, "external_id": item.ExternalID,
			"parent_external_id": item.ParentExternalID, "ref_name": item.RefName, "sha": item.SHA,
			"number": item.Number, "state": item.State, "title": item.Title, "url": item.URL,
			"actor_login": item.ActorLogin, "provenance": json.RawMessage(item.Provenance)})
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_content_records
			(id,space_id,shared_resource_id,provider,external_record_id,parent_external_id,record_type,
			 fingerprint,display_name,mime_type,occurred_at,content,deleted_at)
			VALUES($1,$2,$3,'github',$4,$5,$6,$7,$8,'application/vnd.github+json',$9,$10,$11)
			ON CONFLICT(shared_resource_id,external_record_id) DO UPDATE SET parent_external_id=EXCLUDED.parent_external_id,
			 record_type=EXCLUDED.record_type,fingerprint=EXCLUDED.fingerprint,display_name=EXCLUDED.display_name,
			 occurred_at=EXCLUDED.occurred_at,content=EXCLUDED.content,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`,
			"provider_record_"+uuid.NewString(), item.SpaceID, sharedResourceID,
			item.RecordType+":"+item.ExternalID, item.ParentExternalID, item.RecordType, item.Fingerprint,
			firstNonBlank(item.Title, item.RefName, item.SHA, item.ExternalID), item.OccurredAt, content, item.DeletedAt)
		return err
	})
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (db *Database) GitHubRepositoryRecords(ctx context.Context, userID, spaceID, workspaceID, recordType string, limit int) ([]GitHubRepositoryRecord, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []GitHubRepositoryRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,workspace_id,repository_id,record_type,
			external_id,parent_external_id,ref_name,sha,number,state,title,url,actor_login,fingerprint,
			provenance,occurred_at,deleted_at FROM github_repository_records
			WHERE space_id=$1 AND workspace_id=$2 AND deleted_at IS NULL AND ($3='' OR record_type=$3)
			ORDER BY occurred_at DESC NULLS LAST,updated_at DESC LIMIT $4`, spaceID, workspaceID, recordType, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item GitHubRepositoryRecord
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.WorkspaceID, &item.RepositoryID, &item.RecordType, &item.ExternalID, &item.ParentExternalID, &item.RefName, &item.SHA, &item.Number, &item.State, &item.Title, &item.URL, &item.ActorLogin, &item.Fingerprint, &item.Provenance, &item.OccurredAt, &item.DeletedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) BeginGitHubWebhookDelivery(ctx context.Context, deliveryID, eventName, action string, installationID, repositoryID int64, payloadHash string) (bool, error) {
	inserted := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO github_webhook_deliveries(delivery_id,event_name,action,installation_id,repository_id,payload_sha256)
			VALUES($1,$2,$3,NULLIF($4,0),NULLIF($5,0),$6) ON CONFLICT(delivery_id) DO NOTHING`, deliveryID, eventName, action, installationID, repositoryID, payloadHash)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		inserted = changed == 1
		return nil
	})
	return inserted, err
}

func (db *Database) FinishGitHubWebhookDelivery(ctx context.Context, deliveryID, state, errorCode string) error {
	if !oneOf(state, "processed", "ignored", "failed") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE github_webhook_deliveries SET state=$2,error_code=$3,processed_at=NOW() WHERE delivery_id=$1`, deliveryID, state, errorCode)
		return err
	})
}

func (db *Database) CreateGitHubCredentialHandoff(ctx context.Context, handleHash, userID, spaceID, workspaceID string, expires time.Time) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO github_credential_handoffs(handle_hash,user_id,space_id,workspace_id,expires_at)
			SELECT $1,$2,$3,w.id,$5 FROM github_code_workspaces w WHERE w.id=$4 AND w.space_id=$3 AND w.disabled_at IS NULL`, handleHash, userID, spaceID, workspaceID, expires)
		return err
	})
}

func (db *Database) ConsumeGitHubCredentialHandoff(ctx context.Context, handleHash, userID string) (*GitHubCodeWorkspace, error) {
	out := &GitHubCodeWorkspace{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var workspaceID string
		if err := tx.QueryRowContext(ctx, `UPDATE github_credential_handoffs SET consumed_at=NOW()
			WHERE handle_hash=$1 AND user_id=$2 AND consumed_at IS NULL AND expires_at>NOW() RETURNING workspace_id`, handleHash, userID).Scan(&workspaceID); err != nil {
			return err
		}
		return scanGitHubWorkspace(tx.QueryRowContext(ctx, `SELECT `+githubWorkspaceColumns+` FROM github_code_workspaces WHERE id=$1 AND disabled_at IS NULL`, workspaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ConsumeGitHubCredentialHandoffByHandle is used only by the native bridge.
// The 256-bit, single-use, 90-second handle is the bearer credential; no
// browser cookie or distributable application secret is required.
func (db *Database) ConsumeGitHubCredentialHandoffByHandle(ctx context.Context, handleHash string) (*GitHubCodeWorkspace, string, error) {
	out := &GitHubCodeWorkspace{}
	var userID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var workspaceID string
		if err := tx.QueryRowContext(ctx, `UPDATE github_credential_handoffs SET consumed_at=NOW()
			WHERE handle_hash=$1 AND consumed_at IS NULL AND expires_at>NOW() RETURNING workspace_id,user_id`, handleHash).Scan(&workspaceID, &userID); err != nil {
			return err
		}
		return scanGitHubWorkspace(tx.QueryRowContext(ctx, `SELECT `+githubWorkspaceColumns+` FROM github_code_workspaces WHERE id=$1 AND disabled_at IS NULL`, workspaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrSpaceNotFound
	}
	return out, userID, err
}
