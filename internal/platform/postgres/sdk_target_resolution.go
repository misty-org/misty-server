package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

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
// to be ready. Both paths enforce the same account, provider revision and target scope.
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
		provider, currentVersion, currentInstall, err := sdkConnectedProviderTx(ctx, tx, userID, result.Target.SpaceID, providerID, providerVersion)
		if err != nil {
			return err
		}
		if currentVersion != appVersion || !currentInstall.Equal(installedAt) {
			return ErrSDKProviderUnavailable
		}
		_, officialBrowser := cap.OfficialBrowserProvider(provider.ID, provider.Version)
		if provider.ID != cap.PlannerProviderID && !officialBrowser {
			var availability string
			if err := tx.QueryRowContext(ctx, `SELECT reported_state FROM sdk_provider_registrations WHERE user_id=$1 AND provider_id=$2 AND version=$3 AND enabled FOR SHARE`, userID, providerID, providerVersion).Scan(&availability); err != nil {
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
		if provider.ID == cap.PlannerProviderID {
			if err := requireSpacePermissionTx(ctx, tx, userID, result.Target.SpaceID, PermissionTasksManage); err != nil {
				return err
			}
		}
		if AppAuthorityFromContext(ctx) != nil {
			return ErrAppRuntimeForbidden
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
