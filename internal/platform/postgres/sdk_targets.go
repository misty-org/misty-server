package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

var ErrSDKTargetClarification = errors.New("Choose a Space and an unambiguous destination account before creating a task")

// SDKBackendConnection never crosses the public discovery/result boundary.
type SDKBackendConnection struct {
	SpaceID          string `json:"-"`
	UserID           string `json:"-"`
	AppID            string `json:"-"`
	ID               string `json:"-"`
	Revision         int    `json:"-"`
	EndpointURL      string `json:"-"`
	BearerCiphertext []byte `json:"-"`
	KeyVersion       int    `json:"-"`
}
type SDKBoundCapability struct {
	OwnerUserID string `json:"-"`
	Target      cap.Target
	Provider    cap.Provider
	Definition  cap.Definition
	Connection  SDKBackendConnection `json:"-"`
}

func sdkConnectedProviderTx(ctx context.Context, tx *sql.Tx, userID, spaceID, providerID string, version int) (cap.Provider, string, time.Time, error) {
	if provider, official := cap.OfficialBrowserProvider(providerID, version); official {
		return sdkOfficialBrowserProviderTx(ctx, tx, userID, spaceID, provider)
	}
	if providerID == cap.PlannerProviderID {
		return sdkPlannerProviderTx(ctx, tx, userID, spaceID, version)
	}
	var provider cap.Provider
	var raw []byte
	var appVersion string
	var installedAt time.Time
	query := `SELECT v.definition,r.app_version,r.installed_at FROM sdk_provider_versions v
 JOIN sdk_provider_registrations r ON r.user_id=v.user_id AND r.provider_id=v.provider_id AND r.version=v.version
 WHERE v.user_id=$1 AND v.provider_id=$2 AND v.version=$3 AND r.enabled AND r.reported_state<>'revoked' FOR SHARE OF r`
	err := tx.QueryRowContext(ctx, query, userID, providerID, version).Scan(&raw, &appVersion, &installedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return provider, "", installedAt, ErrSDKProviderUnavailable
	}
	if err != nil {
		return provider, "", installedAt, err
	}
	if cap.Decode(raw, &provider) != nil {
		return provider, "", installedAt, ErrSpaceInvalid
	}
	return provider, appVersion, installedAt, nil
}

func (db *Database) ConfigureSDKBackendConnection(ctx context.Context, userID string, connection SDKBackendConnection, expectedRevision int) (int, error) {
	if AppAuthorityFromContext(ctx) != nil || userID == "" || connection.UserID != userID {
		return 0, ErrAppRuntimeForbidden
	}
	if !cap.ValidID(connection.ID) || expectedRevision < 0 || expectedRevision >= 2147483647 || len(connection.BearerCiphertext) < 17 || len(connection.BearerCiphertext) > 20<<10 || connection.KeyVersion < 1 || len(connection.EndpointURL) > 2048 || !strings.HasPrefix(connection.EndpointURL, "https://") {
		return 0, cap.ErrInvalid
	}
	revision := expectedRevision + 1
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sdk:connection:"+userID+":"+connection.ID); err != nil {
			return err
		}
		var declared bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sdk_provider_versions v JOIN sdk_provider_registrations r ON r.user_id=v.user_id AND r.provider_id=v.provider_id AND r.version=v.version WHERE v.user_id=$1 AND v.app_id=$2 AND r.enabled AND r.reported_state<>'revoked' AND v.definition->'route'->>'kind'='backend' AND v.definition->'route'->>'connectionId'=$3)`, userID, connection.AppID, connection.ID).Scan(&declared); err != nil {
			return err
		}
		if !declared {
			return ErrAppRuntimeForbidden
		}
		var current int
		var appID string
		err := tx.QueryRowContext(ctx, `SELECT revision,app_id FROM sdk_backend_connections WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, connection.ID).Scan(&current, &appID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != expectedRevision {
			return ErrSDKVersionConflict
		}
		if appID != "" && appID != connection.AppID {
			return ErrAppRuntimeForbidden
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sdk_backend_connections(user_id,id,app_id,revision) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,id) DO UPDATE SET revision=EXCLUDED.revision,enabled=TRUE`, userID, connection.ID, connection.AppID, revision); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sdk_backend_connection_versions(user_id,id,revision,endpoint_url,bearer_ciphertext,key_version) VALUES($1,$2,$3,$4,$5,$6)`, userID, connection.ID, revision, connection.EndpointURL, connection.BearerCiphertext, connection.KeyVersion)
		return err
	})
	return revision, err
}

func (db *Database) RevokeSDKBackendConnection(ctx context.Context, userID, appID, connectionID string) error {
	if AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	if !cap.ValidID(connectionID) {
		return cap.ErrInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE sdk_backend_connections SET enabled=FALSE WHERE user_id=$1 AND id=$2 AND app_id=$3`, userID, connectionID, appID)
		return err
	})
}

func (db *Database) ConfigureSDKTarget(ctx context.Context, userID string, request cap.TargetConfiguration) (*cap.Target, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	var target cap.Target
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sdk:target:"+userID+":"+request.TargetID); err != nil {
			return err
		}
		provider, appVersion, installedAt, err := sdkConnectedProviderTx(ctx, tx, userID, request.SpaceID, request.ProviderID, request.ProviderVersion)
		if err != nil {
			return err
		}
		if provider.Route.Kind != "backend" && provider.Route.Kind != "browser" {
			return ErrSDKProviderUnavailable
		}
		if provider.Route.Kind == "backend" && request.Browser != nil {
			return cap.ErrInvalid
		}
		if provider.Route.Kind == "browser" {
			if request.Browser == nil || request.Browser.Validate(provider) != nil {
				return cap.ErrInvalid
			}
			if err := sdkBrowserDeviceAccessTx(ctx, tx, userID, request.Browser.DeviceID); err != nil {
				return err
			}
		}
		if err := sdkTargetSpaceAccessTx(ctx, tx, userID, request.SpaceID); err != nil {
			return err
		}
		var connRevision int
		var connectionID any
		var connectionRevision any
		if provider.Route.Kind == "backend" {
			if err := tx.QueryRowContext(ctx, `SELECT revision FROM sdk_backend_connections WHERE user_id=$1 AND id=$2 AND app_id=$3 AND enabled FOR SHARE`, userID, provider.Route.ConnectionID, strings.Split(provider.ID, "/")[0]).Scan(&connRevision); errors.Is(err, sql.ErrNoRows) {
				return ErrSDKProviderUnavailable
			} else if err != nil {
				return err
			}
			connectionID, connectionRevision = provider.Route.ConnectionID, connRevision
		}
		if len(request.CallerApps) != 0 {
			return ErrAppRuntimeForbidden
		}
		var current int
		err = tx.QueryRowContext(ctx, `SELECT revision FROM sdk_targets WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, request.TargetID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != request.ExpectedRevision {
			return ErrSDKVersionConflict
		}
		revision := current + 1
		binding, _ := json.Marshal(cap.BackendBinding{Kind: "backend", ConnectionID: provider.Route.ConnectionID})
		if provider.Route.Kind == "browser" {
			binding, _ = json.Marshal(request.Browser)
		}
		target = cap.Target{ID: request.TargetID, Revision: revision, AppID: strings.Split(provider.ID, "/")[0], ProviderID: provider.ID, ProviderVersion: provider.Version, SpaceID: request.SpaceID, Label: request.Label, Binding: binding}
		available := map[string]bool{}
		for _, definition := range provider.Capabilities {
			available[definition.Name] = true
		}
		for _, name := range request.Capabilities {
			if !available[name] {
				return ErrAppRuntimeForbidden
			}
		}
		targetJSON, _ := json.Marshal(target)
		capJSON, _ := json.Marshal(request.Capabilities)
		appJSON, _ := json.Marshal(request.CallerApps)
		if _, err := tx.ExecContext(ctx, `INSERT INTO sdk_targets(user_id,id,revision) VALUES($1,$2,$3) ON CONFLICT(user_id,id) DO UPDATE SET revision=EXCLUDED.revision,enabled=TRUE`, userID, target.ID, revision); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sdk_target_versions(user_id,id,revision,provider_id,provider_version,app_version,installed_at,space_id,target,capabilities,caller_apps,connection_id,connection_revision) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,$12,$13)`, userID, target.ID, revision, provider.ID, provider.Version, appVersion, installedAt, target.SpaceID, targetJSON, capJSON, appJSON, connectionID, connectionRevision)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &target, nil
}
func (db *Database) RevokeSDKTarget(ctx context.Context, userID, targetID string) error {
	if AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	if !cap.ValidID(targetID) {
		return cap.ErrInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var revision int
		var enabled bool
		err := tx.QueryRowContext(ctx, `SELECT revision,enabled FROM sdk_targets WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, targetID).Scan(&revision, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil || !enabled {
			return err
		}
		if revision >= 2147483647 {
			return ErrSDKVersionConflict
		}
		// Revocation is a control-plane revision too. A setup request that read
		// the prior enabled target must not silently undo a concurrent revoke.
		// Preserve the old immutable version for effect reconciliation.
		_, err = tx.ExecContext(ctx, `INSERT INTO sdk_target_versions(user_id,id,revision,provider_id,provider_version,app_version,installed_at,space_id,target,capabilities,caller_apps,connection_id,connection_revision)
SELECT user_id,id,$4,provider_id,provider_version,app_version,installed_at,space_id,jsonb_set(target,'{revision}',to_jsonb($4::integer)),capabilities,caller_apps,connection_id,connection_revision
FROM sdk_target_versions WHERE user_id=$1 AND id=$2 AND revision=$3`, userID, targetID, revision, revision+1)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sdk_targets SET enabled=FALSE,revision=$3 WHERE user_id=$1 AND id=$2`, userID, targetID, revision+1)
		return err
	})
}
func sdkTargetSpaceAccessTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if spaceID == "" {
		return nil
	}
	var member bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id WHERE m.user_id=$1 AND m.space_id=$2 AND s.lifecycle_state='active')`, userID, spaceID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return ErrSpaceForbidden
	}
	return nil
}

// ResolveSDKBoundCapability is the common authority check for discovery and every
// future execution/resume. It never chooses a different revision or connection.
