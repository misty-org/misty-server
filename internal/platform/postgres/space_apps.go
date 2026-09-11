package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const PermissionAppsManage = "apps.manage"

var ErrAppDependencies = errors.New("remove dependent apps first")

type SpaceAppInstallation struct {
	SpaceID             string          `json:"space_id"`
	AppID               string          `json:"app_id"`
	State               string          `json:"state"`
	InstalledVersion    string          `json:"installed_version"`
	PermissionVersion   int             `json:"permission_version"`
	GrantedScopes       []string        `json:"granted_scopes"`
	PinRank             int64           `json:"pin_rank"`
	AuthorityGeneration int64           `json:"authority_generation"`
	ReleaseMetadata     json.RawMessage `json:"release_metadata"`
	InstalledAt         time.Time       `json:"installed_at"`
	UninstalledAt       *time.Time      `json:"uninstalled_at,omitempty"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

const spaceAppColumns = `space_id,app_id,state,installed_version,permission_version,granted_scopes,pin_rank,authority_generation,release_metadata,installed_at,uninstalled_at,updated_at`

func scanSpaceApp(row rowScanner) (SpaceAppInstallation, error) {
	var item SpaceAppInstallation
	var scopes []byte
	err := row.Scan(&item.SpaceID, &item.AppID, &item.State, &item.InstalledVersion, &item.PermissionVersion, &scopes, &item.PinRank, &item.AuthorityGeneration, &item.ReleaseMetadata, &item.InstalledAt, &item.UninstalledAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(scopes, &item.GrantedScopes)
	}
	return item, err
}
func (db *Database) SpaceApps(ctx context.Context, userID, spaceID string) ([]SpaceAppInstallation, error) {
	items := []SpaceAppInstallation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAppColumns+` FROM space_app_installations WHERE space_id=$1 ORDER BY pin_rank,app_id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanSpaceApp(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}
func lockSpaceApps(ctx context.Context, tx *sql.Tx, spaceID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space:apps:"+spaceID)
	return err
}
func installSpaceAppTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, spec AppInstallSpec, metadata json.RawMessage) (SpaceAppInstallation, error) {
	if strings.TrimSpace(spec.ID) == "" || spec.Version == "" || spec.PermissionVersion < 1 {
		return SpaceAppInstallation{}, ErrSpaceInvalid
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return SpaceAppInstallation{}, ErrSpaceInvalid
	}
	if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
		return SpaceAppInstallation{}, err
	}
	scopes, err := json.Marshal(spec.Scopes)
	if err != nil {
		return SpaceAppInstallation{}, err
	}
	item, err := scanSpaceApp(tx.QueryRowContext(ctx, `INSERT INTO space_app_installations(space_id,app_id,installed_version,permission_version,granted_scopes,pin_rank,release_metadata,installed_by)
 VALUES($1,$2,$3,$4,$5,COALESCE((SELECT MAX(pin_rank)+1024 FROM space_app_installations WHERE space_id=$1),1024),$6,$7)
 ON CONFLICT(space_id,app_id) DO UPDATE SET state='installed',installed_version=EXCLUDED.installed_version,permission_version=EXCLUDED.permission_version,granted_scopes=EXCLUDED.granted_scopes,release_metadata=EXCLUDED.release_metadata,authority_generation=space_app_installations.authority_generation+1,uninstalled_at=NULL,installed_at=CASE WHEN space_app_installations.state<>'installed' OR space_app_installations.installed_version<>EXCLUDED.installed_version THEN NOW() ELSE space_app_installations.installed_at END,updated_at=NOW()
 RETURNING `+spaceAppColumns, spaceID, spec.ID, spec.Version, spec.PermissionVersion, scopes, metadata, userID))
	if err != nil {
		return item, err
	}
	if err := revokeSpaceAppRuntimeTx(ctx, tx, spaceID, spec.ID); err != nil {
		return item, err
	}
	_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "apps.changed", spec.ID, map[string]any{"state": "installed", "version": spec.Version})
	return item, err
}
func (db *Database) InstallSpaceApp(ctx context.Context, userID, spaceID string, spec AppInstallSpec, metadata json.RawMessage) (*SpaceAppInstallation, error) {
	var item SpaceAppInstallation
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAppsManage); err != nil {
			return err
		}
		if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
			return err
		}
		var release struct {
			RequiresApps []string `json:"requires_apps"`
		}
		if len(metadata) > 0 && json.Unmarshal(metadata, &release) != nil {
			return ErrSpaceInvalid
		}
		for _, dependency := range release.RequiresApps {
			if dependency == spec.ID {
				return ErrAppDependencies
			}
			if err := requireSpaceAppTx(ctx, tx, spaceID, dependency); err != nil {
				return ErrAppDependencies
			}
		}
		var err error
		item, err = installSpaceAppTx(ctx, tx, userID, spaceID, spec, metadata)
		return err
	})
	return &item, err
}
func (db *Database) RemoveSpaceApp(ctx context.Context, userID, spaceID, appID string) (*SpaceAppInstallation, error) {
	var item SpaceAppInstallation
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAppsManage); err != nil {
			return err
		}
		if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
			return err
		}
		var dependent bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_app_installations WHERE space_id=$1 AND state='installed' AND app_id<>$2 AND release_metadata->'requires_apps' ? $2)`, spaceID, appID).Scan(&dependent); err != nil {
			return err
		}
		if dependent {
			return ErrAppDependencies
		}
		var err error
		item, err = scanSpaceApp(tx.QueryRowContext(ctx, `UPDATE space_app_installations SET state='recoverable',uninstalled_at=COALESCE(uninstalled_at,NOW()),updated_at=NOW(),authority_generation=authority_generation+1 WHERE space_id=$1 AND app_id=$2 RETURNING `+spaceAppColumns, spaceID, appID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAppNotInstalled
		}
		if err != nil {
			return err
		}
		if err := revokeSpaceAppRuntimeTx(ctx, tx, spaceID, appID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "apps.changed", appID, map[string]any{"state": "recoverable"})
		return err
	})
	return &item, err
}
func (db *Database) ReorderSpaceApps(ctx context.Context, userID, spaceID string, ids []string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAppsManage); err != nil {
			return err
		}
		if err := lockSpaceApps(ctx, tx, spaceID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_app_installations WHERE space_id=$1 AND state='installed'`, spaceID).Scan(&count); err != nil {
			return err
		}
		if count != len(ids) {
			return ErrSpaceConflict
		}
		seen := map[string]bool{}
		for i, id := range ids {
			if seen[id] {
				return ErrSpaceInvalid
			}
			seen[id] = true
			result, err := tx.ExecContext(ctx, `UPDATE space_app_installations SET pin_rank=$3,updated_at=NOW() WHERE space_id=$1 AND app_id=$2 AND state='installed'`, spaceID, id, (i+1)*1024)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrSpaceConflict
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "apps.changed", spaceID, map[string]any{"order": ids})
		return err
	})
}

// This check also protects non-app callers (including AI and direct APIs).
func requireSpaceAppTx(ctx context.Context, tx *sql.Tx, spaceID, appID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed')`, spaceID, appID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrAppNotInstalled
	}
	return nil
}
func (db *Database) RequireSpaceApp(ctx context.Context, userID, spaceID, appID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return requireSpaceAppTx(ctx, tx, spaceID, appID)
	})
}

// Cancel dependent work while retaining its journal and committed effects. Restoring
// the installation never reenables these runtime registrations or targets.
func revokeSpaceAppRuntimeTx(ctx context.Context, tx *sql.Tx, spaceID, appID string) error {
	statements := []string{
		`DELETE FROM space_device_presence WHERE space_id=$1 AND app_id=$2`,
		`DELETE FROM app_runtime_sessions WHERE space_id=$1 AND app_id=$2`,
		`UPDATE sdk_provider_registrations SET enabled=FALSE WHERE space_id=$1 AND app_id=$2`,
		`UPDATE sdk_targets t SET enabled=FALSE FROM sdk_target_versions v WHERE v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision AND v.space_id=$1 AND v.target->>'appId'=$2`,
		`UPDATE sdk_capability_invocations c SET cancel_requested_at=COALESCE(c.cancel_requested_at,NOW()) FROM ai_invocations i WHERE i.id=c.invocation_id AND i.space_id=$1 AND (c.caller_app_id=$2 OR EXISTS(SELECT 1 FROM sdk_target_versions v WHERE v.user_id=c.user_id AND v.id=c.target_id AND v.revision=c.target_revision AND v.target->>'appId'=$2))`,
	}
	if appID == "agents" {
		if err := cancelMistySpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		statements = append(statements, `UPDATE sdk_capability_invocations c SET cancel_requested_at=COALESCE(c.cancel_requested_at,NOW()) FROM ai_invocations i WHERE i.id=c.invocation_id AND i.space_id=$1 AND $2='agents'`)
	}
	if appID == "chat" {
		statements = append(statements,
			`UPDATE social_outbound_commands SET state='cancelled',last_error_code='space_app_changed',updated_at=NOW() WHERE space_id=$1 AND $2='chat' AND state IN ('queued','sending')`,
			`UPDATE social_scheduled_messages SET status='cancelled',last_error_code='space_app_changed',updated_at=NOW() WHERE space_id=$1 AND $2='chat' AND status IN ('scheduled','queued')`,
			`UPDATE social_automation_rules SET enabled=FALSE,paused_at=NOW(),updated_at=NOW() WHERE space_id=$1 AND $2='chat' AND enabled`)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, spaceID, appID); err != nil {
			return err
		}
	}
	return nil
}
