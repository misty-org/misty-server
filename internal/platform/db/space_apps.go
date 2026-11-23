package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

type SpaceApp struct {
	SpaceID     string          `json:"space_id"`
	AppID       string          `json:"app_id"`
	Version     string          `json:"version"`
	Permissions []string        `json:"permissions"`
	Source      json.RawMessage `json:"source,omitempty"`
	InstalledBy string          `json:"installed_by"`
	InstalledAt time.Time       `json:"installed_at"`
}

const (
	maxSpaceAppsPerSpace  = 64
	maxSpaceAppSourceSize = 64 << 10
)

var spaceAppIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

var spaceAppPermissions = map[string]bool{"files": true, "chat.post": true, "rooms": true, "members": true}

const spaceAppColumns = `space_id,app_id,version,permissions,source,installed_by,installed_at`

func scanSpaceApp(row interface{ Scan(...any) error }, out *SpaceApp) error {
	var permissions pq.StringArray
	var source []byte
	if err := row.Scan(&out.SpaceID, &out.AppID, &out.Version, &permissions, &source, &out.InstalledBy, &out.InstalledAt); err != nil {
		return err
	}
	out.Permissions = []string(permissions)
	if out.Permissions == nil {
		out.Permissions = []string{}
	}
	out.Source = nil
	if len(source) > 0 {
		out.Source = json.RawMessage(source)
	}
	return nil
}

func normalizeSpaceAppSource(source json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) >= maxSpaceAppSourceSize || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, ErrSpaceInvalid
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(trimmed, &object) != nil {
		return nil, ErrSpaceInvalid
	}
	return trimmed, nil
}

func normalizeSpaceAppInput(appID, version string, permissions []string) ([]string, error) {
	if !spaceAppIDPattern.MatchString(appID) || version == "" || utf8.RuneCountInString(version) > 64 {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, permission := range permissions {
		if !spaceAppPermissions[permission] {
			return nil, ErrSpaceInvalid
		}
		if !seen[permission] {
			seen[permission] = true
			clean = append(clean, permission)
		}
	}
	sort.Strings(clean)
	return clean, nil
}

func (db *Database) SpaceApps(ctx context.Context, userID, spaceID string) ([]SpaceApp, error) {
	apps := []SpaceApp{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAppColumns+` FROM space_apps
			WHERE space_id=$1 ORDER BY installed_at,app_id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var app SpaceApp
			if err := scanSpaceApp(rows, &app); err != nil {
				return err
			}
			apps = append(apps, app)
		}
		return rows.Err()
	})
	return apps, err
}

func (db *Database) InstallSpaceApp(ctx context.Context, userID, spaceID, appID, version string, permissions []string, source json.RawMessage) (*SpaceApp, error) {
	permissions, err := normalizeSpaceAppInput(appID, version, permissions)
	if err != nil {
		return nil, err
	}
	storedSource, err := normalizeSpaceAppSource(source)
	if err != nil {
		return nil, err
	}
	var sourceValue any
	if storedSource != nil {
		sourceValue = string(storedSource)
	}
	app := &SpaceApp{}
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, userID, spaces.PermissionAppsManage); err != nil {
			return err
		}
		var installed int
		var already bool
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_apps WHERE space_id=$1`,
			spaceID).Scan(&installed); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_apps WHERE space_id=$1 AND app_id=$2)`,
			spaceID, appID).Scan(&already); err != nil {
			return err
		}
		if !already && installed >= maxSpaceAppsPerSpace {
			return ErrSpaceAppLimit
		}
		if err := scanSpaceApp(tx.QueryRowContext(ctx, `INSERT INTO space_apps(space_id,app_id,version,permissions,source,installed_by)
			VALUES($1,$2,$3,$4,$5::jsonb,$6)
			ON CONFLICT (space_id,app_id) DO UPDATE SET version=EXCLUDED.version,permissions=EXCLUDED.permissions,
				source=EXCLUDED.source,installed_by=EXCLUDED.installed_by,installed_at=NOW()
			RETURNING `+spaceAppColumns, spaceID, appID, version, pq.Array(permissions), sourceValue, userID), app); err != nil {
			return err
		}
		eventType := "app.installed"
		if already {
			eventType = "app.updated"
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, eventType, appID,
			map[string]any{"app_id": appID, "version": version})
		return err
	})
	if err != nil {
		return nil, err
	}
	return app, nil
}

func (db *Database) RemoveSpaceApp(ctx context.Context, userID, spaceID, appID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, userID, spaces.PermissionAppsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_apps WHERE space_id=$1 AND app_id=$2`, spaceID, appID)
		if err != nil {
			return err
		}
		if removed, _ := result.RowsAffected(); removed == 0 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "app.removed", appID, map[string]any{"app_id": appID})
		return err
	})
}

var spaceAppRoomPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func (db *Database) SpaceAppRoomAccess(ctx context.Context, userID, spaceID, appID, roomID string) error {
	if !spaceAppIDPattern.MatchString(appID) || !spaceAppRoomPattern.MatchString(roomID) {
		return ErrSpaceNotFound
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var permissions []string
		err := tx.QueryRowContext(ctx, `SELECT permissions FROM space_apps WHERE space_id=$1 AND app_id=$2`,
			spaceID, appID).Scan(pq.Array(&permissions))
		if err == sql.ErrNoRows {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		for _, permission := range permissions {
			if permission == "rooms" {
				return nil
			}
		}
		return ErrSpaceForbidden
	})
}
