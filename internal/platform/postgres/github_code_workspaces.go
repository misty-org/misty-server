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

type GitHubRepository struct {
	ID            int64           `json:"id"`
	FullName      string          `json:"full_name"`
	DefaultBranch string          `json:"default_branch"`
	CloneURL      string          `json:"clone_url"`
	HTMLURL       string          `json:"html_url"`
	Private       bool            `json:"private"`
	Permissions   json.RawMessage `json:"permissions"`
}

type GitHubCodeWorkspace struct {
	ID                string          `json:"id"`
	SpaceID           string          `json:"space_id"`
	InstallationID    string          `json:"installation_id"`
	SharedResourceID  string          `json:"shared_resource_id"`
	BoundByUserID     string          `json:"bound_by_user_id"`
	RepositoryID      int64           `json:"repository_id"`
	FullName          string          `json:"full_name"`
	DefaultBranch     string          `json:"default_branch"`
	CloneURL          string          `json:"clone_url"`
	HTMLURL           string          `json:"html_url"`
	Private           bool            `json:"private"`
	ClientWorkspaceID string          `json:"client_workspace_id,omitempty"`
	Permissions       json.RawMessage `json:"permissions"`
	SyncCursor        string          `json:"sync_cursor,omitempty"`
	Status            string          `json:"status"`
	LastErrorCode     string          `json:"last_error_code,omitempty"`
	LastSyncedAt      *time.Time      `json:"last_synced_at,omitempty"`
	DisabledAt        *time.Time      `json:"-"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

const githubWorkspaceColumns = `id,space_id,installation_id,shared_resource_id,bound_by_user_id,
	repository_id,full_name,default_branch,clone_url,html_url,private,client_workspace_id,permissions,
	sync_cursor,status,last_error_code,last_synced_at,disabled_at,created_at,updated_at`

func scanGitHubWorkspace(row interface{ Scan(...any) error }, item *GitHubCodeWorkspace) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.InstallationID, &item.SharedResourceID,
		&item.BoundByUserID, &item.RepositoryID, &item.FullName, &item.DefaultBranch,
		&item.CloneURL, &item.HTMLURL, &item.Private, &item.ClientWorkspaceID, &item.Permissions,
		&item.SyncCursor, &item.Status, &item.LastErrorCode, &item.LastSyncedAt, &item.DisabledAt,
		&item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) CreateGitHubCodeWorkspace(ctx context.Context, userID, spaceID, installationRecordID, clientWorkspaceID string, repo GitHubRepository) (*GitHubCodeWorkspace, error) {
	repo.FullName, clientWorkspaceID = strings.TrimSpace(repo.FullName), strings.TrimSpace(clientWorkspaceID)
	if repo.ID <= 0 || repo.FullName == "" || (clientWorkspaceID != "" && (len(clientWorkspaceID) < 8 || len(clientWorkspaceID) > 200)) {
		return nil, ErrSpaceInvalid
	}
	if len(repo.Permissions) == 0 {
		repo.Permissions = json.RawMessage(`{}`)
	}
	out := &GitHubCodeWorkspace{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var integrationID string
		var installationID int64
		if err := tx.QueryRowContext(ctx, `SELECT integration_id,installation_id FROM github_app_installations WHERE id=$1 AND space_id=$2 AND status='active'`, installationRecordID, spaceID).Scan(&integrationID, &installationID); err != nil {
			return ErrSpaceInvalid
		}
		resourceID := "provider_resource_" + uuid.NewString()
		configuration := mustJSON(map[string]any{"installation_id": installationID, "repository_id": repo.ID,
			"full_name": repo.FullName, "default_branch": repo.DefaultBranch, "private": repo.Private})
		if err := tx.QueryRowContext(ctx, `INSERT INTO provider_shared_resources
			(id,space_id,integration_id,published_by_user_id,provider,resource_type,external_resource_id,display_name,permission_scope,configuration)
			VALUES($1,$2,$3,$4,'github','repository',$5,$6,$7,$8)
			ON CONFLICT(space_id,integration_id,provider,resource_type,external_resource_id) DO UPDATE SET
			 display_name=EXCLUDED.display_name,configuration=EXCLUDED.configuration,status='active',last_error_code='',updated_at=NOW()
			RETURNING id`, resourceID, spaceID, integrationID, userID, stringInt64(repo.ID), repo.FullName,
			"github:repository:"+stringInt64(repo.ID), configuration).Scan(&resourceID); err != nil {
			return err
		}
		return scanGitHubWorkspace(tx.QueryRowContext(ctx, `INSERT INTO github_code_workspaces
			(id,space_id,installation_id,shared_resource_id,bound_by_user_id,repository_id,full_name,
			 default_branch,clone_url,html_url,private,client_workspace_id,permissions,status)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'pending')
			ON CONFLICT(space_id,repository_id) DO UPDATE SET installation_id=EXCLUDED.installation_id,
			 shared_resource_id=EXCLUDED.shared_resource_id,bound_by_user_id=EXCLUDED.bound_by_user_id,
			 full_name=EXCLUDED.full_name,default_branch=EXCLUDED.default_branch,clone_url=EXCLUDED.clone_url,
			 html_url=EXCLUDED.html_url,private=EXCLUDED.private,client_workspace_id=EXCLUDED.client_workspace_id,
			 permissions=EXCLUDED.permissions,status='pending',last_error_code='',disabled_at=NULL,updated_at=NOW()
			RETURNING `+githubWorkspaceColumns, "ghws_"+uuid.NewString(), spaceID, installationRecordID,
			resourceID, userID, repo.ID, repo.FullName, repo.DefaultBranch, repo.CloneURL, repo.HTMLURL,
			repo.Private, clientWorkspaceID, repo.Permissions), out)
	})
	return out, err
}

func (db *Database) GitHubCodeWorkspaces(ctx context.Context, userID, spaceID string) ([]GitHubCodeWorkspace, error) {
	items := []GitHubCodeWorkspace{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+githubWorkspaceColumns+` FROM github_code_workspaces WHERE space_id=$1 AND disabled_at IS NULL ORDER BY full_name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item GitHubCodeWorkspace
			if err := scanGitHubWorkspace(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) GitHubCodeWorkspace(ctx context.Context, userID, spaceID, workspaceID string) (*GitHubCodeWorkspace, error) {
	out := &GitHubCodeWorkspace{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanGitHubWorkspace(tx.QueryRowContext(ctx, `SELECT `+githubWorkspaceColumns+` FROM github_code_workspaces WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, workspaceID, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) GitHubCodeWorkspacesForRepository(ctx context.Context, installationID, repositoryID int64) ([]GitHubCodeWorkspace, error) {
	items := []GitHubCodeWorkspace{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+githubPrefixedWorkspaceColumns("w")+` FROM github_code_workspaces w
			JOIN github_app_installations i ON i.id=w.installation_id
			WHERE i.installation_id=$1 AND w.repository_id=$2 AND w.status<>'disabled' AND w.disabled_at IS NULL`, installationID, repositoryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item GitHubCodeWorkspace
			if err := scanGitHubWorkspace(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func githubPrefixedWorkspaceColumns(alias string) string {
	parts := strings.Split(githubWorkspaceColumns, ",")
	for index := range parts {
		parts[index] = alias + "." + strings.TrimSpace(parts[index])
	}
	return strings.Join(parts, ",")
}

func (db *Database) SetGitHubCodeWorkspaceSync(ctx context.Context, workspaceID, cursor, status, errorCode string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE github_code_workspaces SET sync_cursor=$2,status=$3,last_error_code=$4,last_synced_at=NOW(),updated_at=NOW() WHERE id=$1`, workspaceID, cursor, status, errorCode)
		return err
	})
}

func (db *Database) DisableGitHubCodeWorkspace(ctx context.Context, userID, spaceID, workspaceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE github_code_workspaces SET status='disabled',disabled_at=NOW(),updated_at=NOW() WHERE id=$1 AND space_id=$2 AND disabled_at IS NULL`, workspaceID, spaceID)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) DisableGitHubAppInstallation(ctx context.Context, userID, spaceID, installationRecordID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		var integrationID string
		if err := tx.QueryRowContext(ctx, `UPDATE github_app_installations SET status='disabled',updated_at=NOW() WHERE id=$1 AND space_id=$2 AND status<>'disabled' RETURNING integration_id`, installationRecordID, spaceID).Scan(&integrationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_integrations SET status='disabled',updated_at=NOW() WHERE id=$1`, integrationID)
		return err
	})
}
