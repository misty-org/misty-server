package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type GitHubAppSetupState struct {
	UserID, SpaceID, ReturnTo string
	ExpiresAt                 time.Time
}

type GitHubAppInstallation struct {
	ID                  string          `json:"id"`
	SpaceID             string          `json:"space_id"`
	IntegrationID       string          `json:"integration_id"`
	InstalledByUserID   string          `json:"installed_by_user_id"`
	InstallationID      int64           `json:"installation_id"`
	AccountID           int64           `json:"account_id"`
	AccountLogin        string          `json:"account_login"`
	AccountType         string          `json:"account_type"`
	RepositorySelection string          `json:"repository_selection"`
	Permissions         json.RawMessage `json:"permissions"`
	Events              json.RawMessage `json:"events"`
	Status              string          `json:"status"`
	LastErrorCode       string          `json:"last_error_code,omitempty"`
	SuspendedAt         *time.Time      `json:"suspended_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

const githubInstallationColumns = `id,space_id,integration_id,installed_by_user_id,installation_id,
	account_id,account_login,account_type,repository_selection,permissions,events,status,
	last_error_code,suspended_at,created_at,updated_at`

func scanGitHubInstallation(row interface{ Scan(...any) error }, item *GitHubAppInstallation) error {
	return row.Scan(&item.ID, &item.SpaceID, &item.IntegrationID, &item.InstalledByUserID,
		&item.InstallationID, &item.AccountID, &item.AccountLogin, &item.AccountType,
		&item.RepositorySelection, &item.Permissions, &item.Events, &item.Status,
		&item.LastErrorCode, &item.SuspendedAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) CreateGitHubAppSetupState(ctx context.Context, stateHash string, item GitHubAppSetupState) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, item.UserID, item.SpaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO github_app_setup_states(state_hash,user_id,space_id,return_to,expires_at) VALUES($1,$2,$3,$4,$5)`, stateHash, item.UserID, item.SpaceID, item.ReturnTo, item.ExpiresAt)
		return err
	})
}

func (db *Database) ConsumeGitHubAppSetupState(ctx context.Context, stateHash string) (*GitHubAppSetupState, error) {
	out := &GitHubAppSetupState{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE github_app_setup_states SET consumed_at=NOW()
			WHERE state_hash=$1 AND consumed_at IS NULL AND expires_at>NOW()
			RETURNING user_id,space_id,return_to,expires_at`, stateHash).
			Scan(&out.UserID, &out.SpaceID, &out.ReturnTo, &out.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) SaveGitHubAppInstallation(ctx context.Context, userID, spaceID string, item GitHubAppInstallation) (*GitHubAppInstallation, error) {
	item.AccountLogin = strings.TrimSpace(item.AccountLogin)
	if item.InstallationID <= 0 || item.AccountID <= 0 || item.AccountLogin == "" || !oneOf(item.AccountType, "User", "Organization", "Enterprise", "Bot") {
		return nil, ErrSpaceInvalid
	}
	if item.RepositorySelection == "" {
		item.RepositorySelection = "selected"
	}
	if len(item.Permissions) == 0 {
		item.Permissions = json.RawMessage(`{}`)
	}
	if len(item.Events) == 0 {
		item.Events = json.RawMessage(`[]`)
	}
	out := &GitHubAppInstallation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionIntegrationsManage); err != nil {
			return err
		}
		integrationID := "integration_" + uuid.NewString()
		installationRecordID := "ghinst_" + uuid.NewString()
		permissions := []byte(`[]`)
		var permissionMap map[string]string
		if json.Unmarshal(item.Permissions, &permissionMap) == nil {
			values := make([]string, 0, len(permissionMap))
			for name, level := range permissionMap {
				values = append(values, name+":"+level)
			}
			permissions = mustJSON(values)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id)
			VALUES($1,$2,'github',$3,$4,$5,'active',$6)
			ON CONFLICT(space_id,connected_by_user_id,provider,display_name) DO UPDATE SET credential_reference=EXCLUDED.credential_reference,granted_permissions=EXCLUDED.granted_permissions,status='active',updated_at=NOW()`,
			integrationID, spaceID, item.AccountLogin, "github-app-installation:"+stringInt64(item.InstallationID), permissions, userID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM space_integrations WHERE space_id=$1 AND connected_by_user_id=$2 AND provider='github' AND display_name=$3`, spaceID, userID, item.AccountLogin).Scan(&integrationID); err != nil {
			return err
		}
		return scanGitHubInstallation(tx.QueryRowContext(ctx, `INSERT INTO github_app_installations
			(id,space_id,integration_id,installed_by_user_id,installation_id,account_id,account_login,account_type,repository_selection,permissions,events)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT(space_id,installation_id) DO UPDATE SET integration_id=EXCLUDED.integration_id,
			 account_id=EXCLUDED.account_id,account_login=EXCLUDED.account_login,account_type=EXCLUDED.account_type,
			 repository_selection=EXCLUDED.repository_selection,permissions=EXCLUDED.permissions,events=EXCLUDED.events,
			 status='active',last_error_code='',suspended_at=NULL,updated_at=NOW()
			RETURNING `+githubInstallationColumns, installationRecordID, spaceID, integrationID, userID,
			item.InstallationID, item.AccountID, item.AccountLogin, item.AccountType, item.RepositorySelection, item.Permissions, item.Events), out)
	})
	return out, err
}

func stringInt64(value int64) string { return strconv.FormatInt(value, 10) }

func (db *Database) GitHubAppInstallations(ctx context.Context, userID, spaceID string) ([]GitHubAppInstallation, error) {
	items := []GitHubAppInstallation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+githubInstallationColumns+` FROM github_app_installations WHERE space_id=$1 AND status<>'disabled' ORDER BY account_login,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item GitHubAppInstallation
			if err := scanGitHubInstallation(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) GitHubAppInstallation(ctx context.Context, userID, spaceID, id string) (*GitHubAppInstallation, error) {
	out := &GitHubAppInstallation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanGitHubInstallation(tx.QueryRowContext(ctx, `SELECT `+githubInstallationColumns+` FROM github_app_installations WHERE id=$1 AND space_id=$2 AND status='active'`, id, spaceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) UpdateGitHubInstallationLifecycle(ctx context.Context, installationID int64, status, errorCode string) error {
	if !oneOf(status, "active", "suspended", "disabled") {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var integrationID string
		err := tx.QueryRowContext(ctx, `UPDATE github_app_installations SET status=$2,last_error_code=$3,
			suspended_at=CASE WHEN $2='suspended' THEN NOW() ELSE NULL END,updated_at=NOW()
			WHERE installation_id=$1 RETURNING integration_id`, installationID, status, errorCode).Scan(&integrationID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		integrationStatus := "active"
		if status == "suspended" {
			integrationStatus = "needs_attention"
		}
		if status == "disabled" {
			integrationStatus = "disabled"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE space_integrations SET status=$2,updated_at=NOW() WHERE id=$1`, integrationID, integrationStatus); err != nil {
			return err
		}
		workspaceStatus := "active"
		if status == "suspended" {
			workspaceStatus = "needs_attention"
		}
		if status == "disabled" {
			workspaceStatus = "disabled"
		}
		_, err = tx.ExecContext(ctx, `UPDATE github_code_workspaces SET status=$2,last_error_code=$3,
			disabled_at=CASE WHEN $2='disabled' THEN NOW() ELSE disabled_at END,updated_at=NOW()
			WHERE installation_id IN (SELECT id FROM github_app_installations WHERE installation_id=$1) AND disabled_at IS NULL`, installationID, workspaceStatus, errorCode)
		return err
	})
}

func (db *Database) DisableGitHubInstallationRepositories(ctx context.Context, installationID int64, repositoryIDs []int64) error {
	if len(repositoryIDs) == 0 {
		return nil
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE github_code_workspaces SET status='disabled',last_error_code='repository_access_removed',disabled_at=NOW(),updated_at=NOW()
			WHERE installation_id IN (SELECT id FROM github_app_installations WHERE installation_id=$1)
			AND repository_id=ANY($2) AND disabled_at IS NULL RETURNING shared_resource_id`, installationID, pq.Array(repositoryIDs))
		if err != nil {
			return err
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE provider_shared_resources SET status='disabled',last_error_code='repository_access_removed',updated_at=NOW() WHERE id=ANY($1)`, pq.Array(ids))
		}
		return err
	})
}
