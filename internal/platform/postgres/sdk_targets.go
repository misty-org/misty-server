package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/browseractions"
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

func sdkInstalledProviderTx(ctx context.Context, tx *sql.Tx, userID, spaceID, providerID string, version int, requireRegistered bool) (cap.Provider, string, time.Time, error) {
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
	query := `SELECT p.value,i.installed_version,i.installed_at FROM space_app_installations i
 CROSS JOIN LATERAL jsonb_array_elements((i.release_metadata->>'document')::jsonb->'capabilities'->'providers') p(value)
 WHERE i.space_id=$4 AND i.state='installed' AND p.value->>'id'=$2 AND (p.value->>'version')::int=$3
 AND EXISTS(SELECT 1 FROM space_members WHERE space_id=$4 AND user_id=$1)`
	if requireRegistered {
		query += ` AND EXISTS(SELECT 1 FROM sdk_provider_registrations r WHERE r.user_id=$1 AND r.space_id=i.space_id AND r.provider_id=$2 AND r.version=$3 AND r.enabled AND r.reported_state<>'revoked' AND r.app_version=i.installed_version AND r.installed_at=i.installed_at)`
	}
	query += ` FOR SHARE OF i`
	err := tx.QueryRowContext(ctx, query, userID, providerID, version, spaceID).Scan(&raw, &appVersion, &installedAt)
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
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_app_installations i JOIN space_members m ON m.space_id=i.space_id WHERE m.user_id=$1 AND i.app_id=$2 AND i.space_id=$4 AND i.state='installed' AND EXISTS(SELECT 1 FROM jsonb_array_elements((i.release_metadata->>'document')::jsonb->'capabilities'->'providers') p WHERE p->'route'->>'kind'='backend' AND p->'route'->>'connectionId'=$3))`, userID, connection.AppID, connection.ID, connection.SpaceID).Scan(&declared); err != nil {
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
		provider, appVersion, installedAt, err := sdkInstalledProviderTx(ctx, tx, userID, request.SpaceID, request.ProviderID, request.ProviderVersion, true)
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
		for _, appID := range request.CallerApps {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed')`, request.SpaceID, appID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrAppRuntimeForbidden
			}
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
func (db *Database) ResolveSDKBoundCapability(ctx context.Context, userID, targetID string, revision int, name string, capabilityVersion int) (*SDKBoundCapability, error) {
	var result *SDKBoundCapability
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		result, err = resolveSDKBoundCapabilityTx(ctx, tx, userID, targetID, revision, name, capabilityVersion)
		return err
	})
	return result, err
}
func resolveSDKBoundCapabilityTx(ctx context.Context, tx *sql.Tx, userID, targetID string, revision int, name string, capabilityVersion int) (*SDKBoundCapability, error) {
	return resolveSDKBoundCapabilityWithAvailabilityTx(ctx, tx, userID, targetID, revision, name, capabilityVersion, true)
}

// Admission may pin an unavailable implementation, but dispatch must require it
// to be ready. Both paths enforce the same installation, grant and target scope.
func resolveSDKBoundCapabilityWithAvailabilityTx(ctx context.Context, tx *sql.Tx, userID, targetID string, revision int, name string, capabilityVersion int, requireAvailable bool) (*SDKBoundCapability, error) {
	if !cap.ValidID(targetID) || !cap.ValidName(name) || revision < 0 || capabilityVersion < 0 {
		return nil, cap.ErrInvalid
	}
	result := SDKBoundCapability{OwnerUserID: userID}
	err := func() error {

		var targetJSON, capsJSON, appsJSON []byte
		var providerID, appVersion string
		var providerVersion, connRevision int
		var installedAt time.Time
		err := tx.QueryRowContext(ctx, `SELECT v.target,v.capabilities,v.caller_apps,v.provider_id,v.provider_version,v.app_version,v.installed_at,COALESCE(v.connection_revision,0) FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision WHERE t.user_id=$1 AND t.id=$2 AND t.enabled AND ($3=0 OR t.revision=$3) FOR SHARE OF t`, userID, targetID, revision).Scan(&targetJSON, &capsJSON, &appsJSON, &providerID, &providerVersion, &appVersion, &installedAt, &connRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSDKProviderUnavailable
		}
		if err != nil {
			return err
		}
		var allowedCaps, allowedApps []string
		if cap.Decode(targetJSON, &result.Target) != nil || json.Unmarshal(capsJSON, &allowedCaps) != nil || json.Unmarshal(appsJSON, &allowedApps) != nil {
			return ErrSpaceInvalid
		}
		if !slices.Contains(allowedCaps, name) {
			return ErrAppRuntimeForbidden
		}
		provider, currentVersion, currentInstall, err := sdkInstalledProviderTx(ctx, tx, userID, result.Target.SpaceID, providerID, providerVersion, true)
		if err != nil {
			return err
		}
		if currentVersion != appVersion || !currentInstall.Equal(installedAt) {
			return ErrSDKProviderUnavailable
		}
		_, officialBrowser := cap.OfficialBrowserProvider(provider.ID, provider.Version)
		if provider.ID != cap.PlannerProviderID && !officialBrowser {
			var availability string
			if err := tx.QueryRowContext(ctx, `SELECT reported_state FROM sdk_provider_registrations WHERE user_id=$1 AND provider_id=$2 AND version=$3 AND space_id=$4 AND enabled FOR SHARE`, userID, providerID, providerVersion, result.Target.SpaceID).Scan(&availability); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrSDKProviderUnavailable
				}
				return err
			}
			if requireAvailable && availability != "available" {
				return ErrSDKProviderUnavailable
			}
		}
		result.Provider = provider
		var binding cap.BackendBinding
		switch provider.Route.Kind {
		case "server":
			err = result.Target.ValidatePlanner(provider)
			if err == nil {
				err = requireSpacePermissionTx(ctx, tx, userID, result.Target.SpaceID, PermissionTasksManage)
			}
		case "backend":
			binding, err = result.Target.ValidateBackend(provider)
		case "browser":
			var browser cap.BrowserBinding
			browser, err = result.Target.ValidateBrowser(provider)
			if err == nil {
				err = sdkBrowserDeviceAccessTx(ctx, tx, userID, browser.DeviceID)
				if errors.Is(err, ErrDeviceNotFound) {
					err = ErrSDKProviderUnavailable
				}
			}
		default:
			return ErrSDKProviderUnavailable
		}
		if err != nil {
			return err
		}
		found := false
		for _, definition := range provider.Capabilities {
			if definition.Name == name && (capabilityVersion == 0 || capabilityVersion == definition.Version) {
				result.Definition = definition
				found = true
				break
			}
		}
		if !found {
			return ErrSDKProviderUnavailable
		}
		if err := sdkTargetSpaceAccessTx(ctx, tx, userID, result.Target.SpaceID); err != nil {
			return err
		}
		var grantedJSON []byte
		if err := tx.QueryRowContext(ctx, `SELECT granted_scopes FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, result.Target.SpaceID, result.Target.AppID).Scan(&grantedJSON); err != nil {
			return err
		}
		var grants []string
		requiredScopes := result.Definition.RequiredScopes
		if mapped, official := cap.OfficialBrowserScopes(provider.ID, result.Definition.Name); official {
			requiredScopes = mapped
		}
		if json.Unmarshal(grantedJSON, &grants) != nil || !(provider.ID == cap.PlannerProviderID && cap.HasScopes(grants, []string{"tasks.write"}) || cap.HasScopes(grants, requiredScopes)) {
			return ErrAppRuntimeForbidden
		}
		if a := AppAuthorityFromContext(ctx); a != nil {
			if a.UserID != userID || (a.SpaceID == "" || a.SpaceID != result.Target.SpaceID) || !slices.Contains(allowedApps, a.AppID) || !cap.HasScopes(a.Scopes, result.Definition.RequiredScopes) {
				return ErrAppRuntimeForbidden
			}
			var raw []byte
			var generation int64
			if err := tx.QueryRowContext(ctx, `SELECT granted_scopes,authority_generation FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, a.SpaceID, a.AppID).Scan(&raw, &generation); errors.Is(err, sql.ErrNoRows) {
				return ErrAppRuntimeForbidden
			} else if err != nil {
				return err
			}
			if a.Generation <= 0 || a.Generation != generation {
				return ErrAppRuntimeForbidden
			}
			var current []string
			if json.Unmarshal(raw, &current) != nil || !cap.HasScopes(current, result.Definition.RequiredScopes) {
				return ErrAppRuntimeForbidden
			}
		}
		if provider.Route.Kind == "browser" {
			if _, err := browseractions.Pilots.Resolve(provider, name); err != nil {
				return ErrSDKProviderUnavailable
			}
			browser, err := result.Target.ValidateBrowser(provider)
			if err != nil || browser.ScopeID == "" || browser.AccountIdentity == "" {
				return ErrSDKProviderUnavailable
			}
			return nil
		}
		if provider.ID == cap.PlannerProviderID {
			return nil
		}
		connection := SDKBackendConnection{UserID: userID, AppID: result.Target.AppID, ID: binding.ConnectionID, Revision: connRevision}
		err = tx.QueryRowContext(ctx, `SELECT v.endpoint_url,v.bearer_ciphertext,v.key_version FROM sdk_backend_connections c JOIN sdk_backend_connection_versions v ON v.user_id=c.user_id AND v.id=c.id AND v.revision=c.revision WHERE c.user_id=$1 AND c.id=$2 AND c.app_id=$3 AND c.revision=$4 AND c.enabled FOR SHARE OF c`, userID, binding.ConnectionID, result.Target.AppID, connRevision).Scan(&connection.EndpointURL, &connection.BearerCiphertext, &connection.KeyVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSDKProviderUnavailable
		}
		if err != nil {
			return err
		}
		result.Connection = connection
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) ResolveSDKTargets(ctx context.Context, userID string, request cap.TargetResolve) ([]cap.Target, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	a := AppAuthorityFromContext(ctx)
	if a != nil {
		if err := db.ValidateAppExecutionAuthority(ctx, a, userID, a.SpaceID, "capabilities.read"); err != nil {
			return nil, err
		}
	}
	if request.SpaceID == "" && a != nil {
		request.SpaceID = a.SpaceID
	}
	if request.Capability == "tasks.create" && request.TargetID == "" {
		if request.SpaceID == "" {
			return nil, ErrSDKTargetClarification
		}
		if request.ProviderID == "" {
			request.ProviderID = cap.PlannerProviderID
		}
	}
	targets := []cap.Target{}
	if request.ContextID != "" {
		return targets, ErrSDKProviderUnavailable
	}
	ids := []string{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if request.SpaceID != "" {
			if err := sdkTargetSpaceAccessTx(ctx, tx, userID, request.SpaceID); err != nil {
				return err
			}
			if err := ensureSDKPlannerTargetTx(ctx, tx, userID, request.SpaceID); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT t.id FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision WHERE t.user_id=$1 AND t.enabled AND ($2='' OR t.id::text=$2) AND v.capabilities ? $3 AND ($4='' OR v.space_id=$4) AND ($5='' OR v.provider_id=$5) ORDER BY t.id`, userID, request.TargetID, request.Capability, request.SpaceID, request.ProviderID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		resolved, err := db.ResolveSDKBoundCapability(ctx, userID, id, 0, request.Capability, 0)
		if errors.Is(err, ErrSDKProviderUnavailable) || errors.Is(err, ErrAppRuntimeForbidden) || errors.Is(err, ErrSpaceForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(targets) == 100 {
			return nil, ErrSpaceLimit
		}
		targets = append(targets, resolved.Target)
	}
	if request.Capability == "tasks.create" && len(targets) > 1 {
		return nil, ErrSDKTargetClarification
	}
	return targets, nil
}

func (db *Database) discoverSDKTargetProvider(ctx context.Context, userID, targetID, capability string) (*cap.Provider, error) {
	if !cap.ValidID(targetID) {
		return nil, cap.ErrInvalid
	}
	var raw []byte
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT p.definition FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision JOIN sdk_provider_versions p ON p.user_id=v.user_id AND p.provider_id=v.provider_id AND p.version=v.provider_version WHERE t.user_id=$1 AND t.id=$2 AND t.enabled`, userID, targetID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var provider cap.Provider
	if cap.Decode(raw, &provider) != nil {
		return nil, ErrSpaceInvalid
	}
	allowed := []cap.Definition{}
	for _, definition := range provider.Capabilities {
		if capability != "" && capability != definition.Name {
			continue
		}
		_, err := db.ResolveSDKBoundCapability(ctx, userID, targetID, 0, definition.Name, definition.Version)
		if errors.Is(err, ErrSDKProviderUnavailable) || errors.Is(err, ErrAppRuntimeForbidden) || errors.Is(err, ErrSpaceForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		allowed = append(allowed, definition)
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	provider.Capabilities = allowed
	return &provider, nil
}

func sdkBrowserDeviceAccessTx(ctx context.Context, tx *sql.Tx, userID, deviceID string) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, userID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrDeviceNotFound
	}
	return nil
}

// SDKTargetsForControl does not imply execution availability and never filters
// away disabled targets needed for review. App bearers cannot inspect user grants.
func (db *Database) SDKTargetsForControl(ctx context.Context, userID, after string, limit int) (*cap.TargetPage, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if after != "" && !cap.ValidID(after) || limit < 1 || limit > 100 {
		return nil, cap.ErrInvalid
	}
	page := &cap.TargetPage{Targets: []cap.TargetRecord{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT v.target,v.capabilities,v.caller_apps,t.enabled FROM sdk_targets t JOIN sdk_target_versions v ON v.user_id=t.user_id AND v.id=t.id AND v.revision=t.revision WHERE t.user_id=$1 AND ($2='' OR t.id>NULLIF($2,'')::uuid) ORDER BY t.id LIMIT $3`, userID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item cap.TargetRecord
			var target, caps, apps []byte
			if err := rows.Scan(&target, &caps, &apps, &item.Enabled); err != nil {
				return err
			}
			if cap.Decode(target, &item.Target) != nil || json.Unmarshal(caps, &item.Capabilities) != nil || json.Unmarshal(apps, &item.CallerApps) != nil {
				return ErrSpaceInvalid
			}
			if len(page.Targets) == limit {
				last := page.Targets[limit-1].Target.ID
				page.NextCursor = &last
				break
			}
			page.Targets = append(page.Targets, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}
