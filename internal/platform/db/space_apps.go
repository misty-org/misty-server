package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

// SpaceApp is a third-party app a Space has installed. Misty's own apps are built in and never recorded
// here. The bundle itself comes from the host's catalog; the server only records which app, at which
// version, with the permissions a mod approved.
type SpaceApp struct {
	SpaceID     string    `json:"space_id"`
	AppID       string    `json:"app_id"`
	Version     string    `json:"version"`
	Permissions []string  `json:"permissions"`
	InstalledBy string    `json:"installed_by"`
	InstalledAt time.Time `json:"installed_at"`
}

// SpaceAppValue is one entry of an app's storage in a Space.
type SpaceAppValue struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const (
	maxSpaceAppValueBytes = 64 << 10
	maxSpaceAppKeyRunes   = 200
	maxSpaceAppKeys       = 1000
	maxSpaceAppsPerSpace  = 64
)

// builtinSpaceApps are Misty's own apps, which every Space has without installing them. They are never
// recorded, so a request to install one is refused.
var builtinSpaceApps = map[string]bool{
	"docs": true, "whiteboards": true, "lab": true, "planner": true, "calendar": true,
	"media": true, "code": true, "slides": true, "data": true,
}

var spaceAppIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// spaceAppPermissions are the only permissions an app may ask for (misty-app.json).
var spaceAppPermissions = map[string]bool{"files": true, "chat.post": true, "rooms": true, "records": true, "members": true}

const spaceAppColumns = `space_id,app_id,version,permissions,installed_by,installed_at`

func scanSpaceApp(row interface{ Scan(...any) error }, out *SpaceApp) error {
	var permissions pq.StringArray
	if err := row.Scan(&out.SpaceID, &out.AppID, &out.Version, &permissions, &out.InstalledBy, &out.InstalledAt); err != nil {
		return err
	}
	out.Permissions = []string(permissions)
	if out.Permissions == nil {
		out.Permissions = []string{}
	}
	return nil
}

func normalizeSpaceAppInput(appID, version string, permissions []string) ([]string, error) {
	if !spaceAppIDPattern.MatchString(appID) || builtinSpaceApps[appID] || version == "" || utf8.RuneCountInString(version) > 64 {
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

// SpaceApps lists a Space's installed apps, oldest first. Every member may read it.
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

// InstallSpaceApp installs an app, or moves an installed one to a new version and permissions. Only a
// member who holds apps.manage (a mod) may. The catalog's check of the version is the host's.
func (db *Database) InstallSpaceApp(ctx context.Context, userID, spaceID, appID, version string, permissions []string) (*SpaceApp, error) {
	permissions, err := normalizeSpaceAppInput(appID, version, permissions)
	if err != nil {
		return nil, err
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
		if err := scanSpaceApp(tx.QueryRowContext(ctx, `INSERT INTO space_apps(space_id,app_id,version,permissions,installed_by)
			VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (space_id,app_id) DO UPDATE SET version=EXCLUDED.version,permissions=EXCLUDED.permissions,
				installed_by=EXCLUDED.installed_by,installed_at=NOW()
			RETURNING `+spaceAppColumns, spaceID, appID, version, pq.Array(permissions), userID), app); err != nil {
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

// RemoveSpaceApp uninstalls an app and drops its storage. The Space's files are never touched: what an app
// saved lives in the Space's Library, not in the app.
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

// requireInstalledSpaceAppTx checks the caller is a member and the app is installed.
func requireInstalledSpaceAppTx(ctx context.Context, tx *sql.Tx, spaceID, userID, appID string) error {
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		return err
	}
	var installed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_apps WHERE space_id=$1 AND app_id=$2)`,
		spaceID, appID).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return ErrSpaceNotFound
	}
	return nil
}

// SpaceAppValueGet reads one key of an app's storage; a key never set is ErrSpaceNotFound.
func (db *Database) SpaceAppValueGet(ctx context.Context, userID, spaceID, appID, key string) (*SpaceAppValue, error) {
	out := &SpaceAppValue{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireInstalledSpaceAppTx(ctx, tx, spaceID, userID, appID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT key,value,updated_at FROM space_app_storage
			WHERE space_id=$1 AND app_id=$2 AND key=$3`, spaceID, appID, key).Scan(&out.Key, &out.Value, &out.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SpaceAppValues lists an app's storage, optionally only keys that start with prefix.
func (db *Database) SpaceAppValues(ctx context.Context, userID, spaceID, appID, prefix string) ([]SpaceAppValue, error) {
	values := []SpaceAppValue{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireInstalledSpaceAppTx(ctx, tx, spaceID, userID, appID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT key,value,updated_at FROM space_app_storage
			WHERE space_id=$1 AND app_id=$2 AND starts_with(key,$3) ORDER BY key LIMIT $4`,
			spaceID, appID, prefix, maxSpaceAppKeys)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value SpaceAppValue
			if err := rows.Scan(&value.Key, &value.Value, &value.UpdatedAt); err != nil {
				return err
			}
			values = append(values, value)
		}
		return rows.Err()
	})
	return values, err
}

// SpaceAppValueSet writes one key. The value must be JSON of at most 64 KB; an app holds at most 1000 keys.
func (db *Database) SpaceAppValueSet(ctx context.Context, userID, spaceID, appID, key string, value json.RawMessage) (*SpaceAppValue, error) {
	if key == "" || utf8.RuneCountInString(key) > maxSpaceAppKeyRunes || len(value) == 0 ||
		len(value) > maxSpaceAppValueBytes || !json.Valid(value) {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceAppValue{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireInstalledSpaceAppTx(ctx, tx, spaceID, userID, appID); err != nil {
			return err
		}
		var exists bool
		var keys int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), COALESCE(bool_or(key=$3),false) FROM space_app_storage
			WHERE space_id=$1 AND app_id=$2`, spaceID, appID, key).Scan(&keys, &exists); err != nil {
			return err
		}
		if !exists && keys >= maxSpaceAppKeys {
			return ErrSpaceAppLimit
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_app_storage(space_id,app_id,key,value) VALUES($1,$2,$3,$4)
			ON CONFLICT (space_id,app_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()
			RETURNING key,value,updated_at`, spaceID, appID, key, []byte(value)).Scan(&out.Key, &out.Value, &out.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SpaceAppValueRemove deletes one key; removing a key that isn't there is not an error.
func (db *Database) SpaceAppValueRemove(ctx context.Context, userID, spaceID, appID, key string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireInstalledSpaceAppTx(ctx, tx, spaceID, userID, appID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM space_app_storage WHERE space_id=$1 AND app_id=$2 AND key=$3`,
			spaceID, appID, key)
		return err
	})
}
