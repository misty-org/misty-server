package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const AppDataRecoveryPeriod = 30 * 24 * time.Hour

var (
	ErrAppNotFound       = errors.New("app installation not found")
	ErrAppNotInstalled   = errors.New("app is not installed")
	ErrAppAlreadyPurging = errors.New("app data is already being purged")
)

type UserAppInstallation struct {
	AppID             string     `json:"app_id"`
	State             string     `json:"state"`
	InstalledVersion  string     `json:"installed_version"`
	PermissionVersion int        `json:"permission_version"`
	GrantedScopes     []string   `json:"granted_scopes"`
	Pinned            bool       `json:"pinned"`
	PinRank           int64      `json:"pin_rank"`
	InstalledAt       time.Time  `json:"installed_at"`
	UninstalledAt     *time.Time `json:"uninstalled_at,omitempty"`
	DataDeletionAt    *time.Time `json:"data_deletion_at,omitempty"`
	PurgedAt          *time.Time `json:"purged_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (db *Database) UserApps(ctx context.Context, userID string) ([]UserAppInstallation, error) {
	items := []UserAppInstallation{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT app_id,state,installed_version,permission_version,
			granted_scopes,pinned,pin_rank,installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at
			FROM user_app_installations WHERE user_id=$1 AND state<>'purged'
			ORDER BY CASE WHEN state='installed' THEN 0 ELSE 1 END,pinned DESC,pin_rank,app_id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanUserApp(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) InstallUserApp(
	ctx context.Context,
	userID, appID, version string,
	permissionVersion int,
	scopes []string,
) (*UserAppInstallation, error) {
	appID = strings.TrimSpace(appID)
	version = strings.TrimSpace(version)
	if userID == "" || appID == "" || len(appID) > 80 || version == "" || len(version) > 40 || permissionVersion < 1 {
		return nil, ErrSpaceInvalid
	}
	encodedScopes, err := json.Marshal(scopes)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	var result UserAppInstallation
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		item, err := installUserAppTx(ctx, tx, userID, appID, version, permissionVersion, encodedScopes)
		result = item
		return err
	})
	return &result, err
}

func installUserAppTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, appID, version string,
	permissionVersion int,
	encodedScopes []byte,
) (UserAppInstallation, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "apps:install:"+userID+":"+appID); err != nil {
		return UserAppInstallation{}, err
	}
	previousState, previousVersion := "", ""
	err := tx.QueryRowContext(ctx, `SELECT state,installed_version FROM user_app_installations WHERE user_id=$1 AND app_id=$2`, userID, appID).Scan(&previousState, &previousVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserAppInstallation{}, err
	}
	if previousState == "purging" {
		return UserAppInstallation{}, ErrAppAlreadyPurging
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO user_app_installations
		(user_id,app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank)
		VALUES($1,$2,'installed',$3,$4,$5::jsonb,TRUE,
			COALESCE((SELECT MAX(pin_rank)+1024 FROM user_app_installations WHERE user_id=$1 AND state='installed'),1024))
		ON CONFLICT(user_id,app_id) DO UPDATE SET
			state='installed',installed_version=EXCLUDED.installed_version,
			permission_version=EXCLUDED.permission_version,granted_scopes=EXCLUDED.granted_scopes,
			pinned=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.pinned ELSE TRUE END,
			pin_rank=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.pin_rank ELSE EXCLUDED.pin_rank END,
			installed_at=CASE WHEN user_app_installations.state='installed' THEN user_app_installations.installed_at ELSE NOW() END,
			uninstalled_at=NULL,data_deletion_at=NULL,purged_at=NULL,updated_at=NOW()
		RETURNING app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank,
			installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at`, userID, appID, version, permissionVersion, encodedScopes)
	item, err := scanUserApp(row)
	if err != nil {
		return UserAppInstallation{}, err
	}
	// A reviewed permission change retires the previous token. Issuance holds a
	// shared lock on this installation row so an old grant cannot be minted
	// after this update commits. Regranting later must not revive retired tokens.
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_runtime_sessions
		WHERE user_id=$1 AND app_id=$2 AND (scopes <> $3::jsonb OR $4)`, userID, appID, encodedScopes, previousVersion != "" && previousVersion != version); err != nil {
		return UserAppInstallation{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_data_deletion_jobs WHERE user_id=$1 AND app_id=$2`, userID, appID); err != nil {
		return UserAppInstallation{}, err
	}
	eventType := "installed"
	if previousState == "recoverable" {
		eventType = "restored"
	} else if previousState == "installed" && previousVersion != version {
		eventType = "updated"
	}
	if err := recordAppInstallEventTx(ctx, tx, userID, appID, eventType, map[string]any{
		"version": version, "permission_version": permissionVersion,
	}); err != nil {
		return UserAppInstallation{}, err
	}
	return item, nil
}

func (db *Database) SetUserAppPinned(ctx context.Context, userID, appID string, pinned bool) (*UserAppInstallation, error) {
	var result UserAppInstallation
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `UPDATE user_app_installations SET pinned=$3,
			pin_rank=CASE WHEN $3 AND NOT pinned THEN COALESCE((SELECT MAX(i.pin_rank)+1024 FROM user_app_installations i
				WHERE i.user_id=$1 AND i.state='installed' AND i.app_id<>$2),1024) ELSE pin_rank END,
			updated_at=NOW()
			WHERE user_id=$1 AND app_id=$2 AND state='installed'
			RETURNING app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank,
				installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at`, userID, appID, pinned)
		item, err := scanUserApp(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAppNotInstalled
		}
		if err != nil {
			return err
		}
		result = item
		eventType := "unpinned"
		if pinned {
			eventType = "pinned"
		}
		return recordAppInstallEventTx(ctx, tx, userID, appID, eventType, nil)
	})
	return &result, err
}

func (db *Database) UninstallUserApp(ctx context.Context, userID, appID string, now time.Time) (*UserAppInstallation, error) {
	var result UserAppInstallation
	now = now.UTC()
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "apps:uninstall:"+userID+":"+appID); err != nil {
			return err
		}
		currentRow := tx.QueryRowContext(ctx, `SELECT app_id,state,installed_version,permission_version,granted_scopes,
			pinned,pin_rank,installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at
			FROM user_app_installations WHERE user_id=$1 AND app_id=$2 FOR UPDATE`, userID, appID)
		current, err := scanUserApp(currentRow)
		if errors.Is(err, sql.ErrNoRows) || current.State == "purged" {
			return ErrAppNotFound
		}
		if err != nil {
			return err
		}
		if current.State == "recoverable" {
			result = current
			return nil
		}
		if current.State == "purging" {
			return ErrAppAlreadyPurging
		}
		// Reinstall must not revive a still-unexpired credential from this install.
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_runtime_sessions WHERE user_id=$1 AND app_id=$2`, userID, appID); err != nil {
			return err
		}
		deleteAt := now.Add(AppDataRecoveryPeriod)
		row := tx.QueryRowContext(ctx, `UPDATE user_app_installations SET state='recoverable',pinned=FALSE,
			uninstalled_at=$3,data_deletion_at=$4,purged_at=NULL,updated_at=$3
			WHERE user_id=$1 AND app_id=$2 AND state='installed'
			RETURNING app_id,state,installed_version,permission_version,granted_scopes,pinned,pin_rank,
				installed_at,uninstalled_at,data_deletion_at,purged_at,updated_at`, userID, appID, now, deleteAt)
		item, err := scanUserApp(row)
		if err != nil {
			return err
		}
		result = item
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_data_deletion_jobs(user_id,app_id,delete_at)
			VALUES($1,$2,$3) ON CONFLICT(user_id,app_id) DO UPDATE SET
			delete_at=EXCLUDED.delete_at,state='pending',attempts=0,last_error='',started_at=NULL,completed_at=NULL,updated_at=NOW()`, userID, appID, deleteAt); err != nil {
			return err
		}
		return recordAppInstallEventTx(ctx, tx, userID, appID, "uninstalled", map[string]any{"data_deletion_at": deleteAt})
	})
	return &result, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUserApp(row rowScanner) (UserAppInstallation, error) {
	var item UserAppInstallation
	var scopes []byte
	err := row.Scan(&item.AppID, &item.State, &item.InstalledVersion, &item.PermissionVersion,
		&scopes, &item.Pinned, &item.PinRank, &item.InstalledAt, &item.UninstalledAt,
		&item.DataDeletionAt, &item.PurgedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(scopes, &item.GrantedScopes); err != nil {
		return item, err
	}
	if item.GrantedScopes == nil {
		item.GrantedScopes = []string{}
	}
	return item, nil
}

func recordAppInstallEventTx(ctx context.Context, tx *sql.Tx, userID, appID, eventType string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_install_events(user_id,app_id,event_type,metadata)
		VALUES($1,$2,$3,$4::jsonb)`, userID, appID, eventType, encoded)
	return err
}
