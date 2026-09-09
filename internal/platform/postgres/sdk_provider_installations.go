package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/appcatalog"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

var ErrSDKVersionConflict = errors.New("immutable SDK version conflicts with installed content")
var ErrSDKPublisherChanged = errors.New("SDK publisher differs from the installed key")
var ErrSDKProviderUnavailable = errors.New("SDK provider is unavailable")

// InstallVerifiedSDKApp is called only by trusted account controls after reviewing
// this exact document. App credentials and agent execution principals are denied.
// A first install pins the reviewed public key; it does not certify its publisher.
func (db *Database) InstallVerifiedSDKApp(ctx context.Context, userID string, signed cap.SignedManifest, reviewedDigest string) (*UserAppInstallation, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	verified, err := cap.Verify(signed)
	if err != nil {
		return nil, err
	}
	if reviewedDigest != verified.Digest {
		return nil, ErrAppRuntimeForbidden
	}
	if _, official := appcatalog.Find(verified.AppID); official {
		return nil, ErrAppRuntimeForbidden
	}
	var result UserAppInstallation
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "apps:install:"+userID+":"+verified.AppID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sdk:contracts:"+userID); err != nil {
			return err
		}
		var oldKey []byte
		err := tx.QueryRowContext(ctx, `SELECT public_key FROM sdk_app_publishers WHERE user_id=$1 AND app_id=$2`, userID, verified.AppID).Scan(&oldKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if len(oldKey) > 0 && !bytes.Equal(oldKey, verified.PublicKey) {
			return ErrSDKPublisherChanged
		}
		var oldScopes []byte
		var oldPermission int
		err = tx.QueryRowContext(ctx, `SELECT granted_scopes,permission_version FROM user_app_installations WHERE user_id=$1 AND app_id=$2 FOR UPDATE`, userID, verified.AppID).Scan(&oldScopes, &oldPermission)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && len(oldKey) == 0 {
			return ErrSDKPublisherChanged
		}
		scopes, _ := json.Marshal(verified.Scopes)
		if len(oldScopes) > 0 && !cap.EqualJSON(oldScopes, scopes) && verified.PermissionVersion <= oldPermission {
			return ErrSDKVersionConflict
		}
		var oldDigest string
		err = tx.QueryRowContext(ctx, `SELECT digest FROM sdk_app_manifest_versions WHERE user_id=$1 AND app_id=$2 AND app_version=$3`, userID, verified.AppID, verified.Version).Scan(&oldDigest)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if oldDigest != "" && oldDigest != verified.Digest {
			return ErrSDKVersionConflict
		}
		// Re-register after a version or consent change. A later rollback must
		// not resurrect an older registration merely by matching its version.
		if _, err := tx.ExecContext(ctx, `UPDATE sdk_provider_registrations r SET enabled=FALSE,updated_at=NOW()
          FROM user_app_installations i WHERE i.user_id=$1 AND i.app_id=$2 AND r.user_id=i.user_id AND r.app_id=i.app_id
          AND (i.installed_version<>$3 OR i.granted_scopes<>$4::jsonb OR i.state<>'installed')`, userID, verified.AppID, verified.Version, scopes); err != nil {
			return err
		}
		result, err = installUserAppTx(ctx, tx, userID, verified.AppID, verified.Version, verified.PermissionVersion, scopes)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sdk_app_publishers(user_id,app_id,public_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, verified.AppID, verified.PublicKey); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sdk_app_manifest_versions(user_id,app_id,app_version,digest,document,signature) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, userID, verified.AppID, verified.Version, verified.Digest, verified.Document, verified.Signature); err != nil {
			return err
		}
		for _, provider := range verified.Capabilities.Providers {
			raw, _ := json.Marshal(provider)
			var matches bool
			if _, err = tx.ExecContext(ctx, `INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, userID, provider.ID, provider.Version, verified.AppID, raw); err != nil {
				return err
			}
			if err = tx.QueryRowContext(ctx, `SELECT definition=$4::jsonb AND app_id=$5 FROM sdk_provider_versions WHERE user_id=$1 AND provider_id=$2 AND version=$3`, userID, provider.ID, provider.Version, raw, verified.AppID).Scan(&matches); err != nil {
				return err
			}
			if !matches {
				return ErrSDKVersionConflict
			}
			for _, definition := range provider.Capabilities {
				raw, _ := json.Marshal(definition)
				if _, err = tx.ExecContext(ctx, `INSERT INTO sdk_capability_contract_versions(user_id,name,version,definition) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, userID, definition.Name, definition.Version, raw); err != nil {
					return err
				}
				if err = tx.QueryRowContext(ctx, `SELECT definition=$4::jsonb FROM sdk_capability_contract_versions WHERE user_id=$1 AND name=$2 AND version=$3`, userID, definition.Name, definition.Version, raw).Scan(&matches); err != nil {
					return err
				}
				if !matches {
					return ErrSDKVersionConflict
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) IsVerifiedSDKAppInstalled(ctx context.Context, userID, appID string) (bool, error) {
	var exists bool
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_app_installations i JOIN sdk_app_manifest_versions m ON m.user_id=i.user_id AND m.app_id=i.app_id AND m.app_version=i.installed_version WHERE i.user_id=$1 AND i.app_id=$2 AND i.state='installed')`, userID, appID).Scan(&exists)
	})
	return exists, err
}

// providerAuthorityTx holds the installation lock through the mutation. Uninstall
// and consent changes cannot commit between the permission check and registration.
func providerAuthorityTx(ctx context.Context, tx *sql.Tx, userID string) (*AppExecutionAuthority, []string, error) {
	a := AppAuthorityFromContext(ctx)
	if a == nil || a.UserID != userID || a.SpaceID != "" || !slices.Contains(a.Scopes, "capabilities.providers.write") {
		return nil, nil, ErrAppRuntimeForbidden
	}
	var scopes []byte
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT granted_scopes,authority_generation FROM user_app_installations WHERE user_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, userID, a.AppID).Scan(&scopes, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrAppRuntimeForbidden
	}
	if err != nil {
		return nil, nil, err
	}
	if a.Generation <= 0 || a.Generation != generation {
		return nil, nil, ErrAppRuntimeForbidden
	}
	var current []string
	if json.Unmarshal(scopes, &current) != nil || !slices.Contains(current, "capabilities.providers.write") {
		return nil, nil, ErrAppRuntimeForbidden
	}
	return a, current, nil
}

func (db *Database) RegisterSDKProvider(ctx context.Context, userID, digest string, provider cap.Provider) (*cap.Availability, error) {
	result := cap.Availability{State: "unavailable", ObservedAt: time.Now().UTC(), Reason: "Provider registered; target availability has not been verified."}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		a, scopes, err := providerAuthorityTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		if err := provider.Validate(a.AppID, scopes); err != nil {
			return err
		}
		for _, definition := range provider.Capabilities {
			for _, scope := range definition.RequiredScopes {
				if !slices.Contains(a.Scopes, scope) {
					return ErrAppRuntimeForbidden
				}
			}
		}
		var version, document, installedDigest string
		var installedAt time.Time
		err = tx.QueryRowContext(ctx, `SELECT i.installed_version,m.document,m.digest,i.installed_at FROM user_app_installations i JOIN sdk_app_manifest_versions m ON m.user_id=i.user_id AND m.app_id=i.app_id AND m.app_version=i.installed_version WHERE i.user_id=$1 AND i.app_id=$2 AND i.state='installed'`, userID, a.AppID).Scan(&version, &document, &installedDigest, &installedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAppRuntimeForbidden
		}
		if err != nil {
			return err
		}
		if digest != installedDigest {
			return ErrSDKVersionConflict
		}
		var manifest cap.InstallDocument
		if cap.Decode([]byte(document), &manifest) != nil {
			return ErrSpaceInvalid
		}
		raw, _ := json.Marshal(provider)
		match := false
		for _, candidate := range manifest.Capabilities.Providers {
			expected, _ := json.Marshal(candidate)
			if cap.EqualJSON(raw, expected) {
				match = true
				break
			}
		}
		if !match {
			return ErrSDKVersionConflict
		}
		// Retrying the same registration preserves a newer availability observation.
		_, err = tx.ExecContext(ctx, `INSERT INTO sdk_provider_registrations(user_id,provider_id,version,app_id,app_version,installed_at,reported_state,observed_at,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
   ON CONFLICT(user_id,provider_id) DO UPDATE SET version=EXCLUDED.version,app_version=EXCLUDED.app_version,installed_at=EXCLUDED.installed_at,enabled=TRUE,reported_state=EXCLUDED.reported_state,observed_at=EXCLUDED.observed_at,reason=EXCLUDED.reason,updated_at=NOW()
   WHERE NOT sdk_provider_registrations.enabled OR sdk_provider_registrations.version<>EXCLUDED.version OR sdk_provider_registrations.app_version<>EXCLUDED.app_version OR sdk_provider_registrations.installed_at<>EXCLUDED.installed_at`, userID, provider.ID, provider.Version, a.AppID, version, installedAt, result.State, result.ObservedAt, result.Reason)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT reported_state,observed_at,reason FROM sdk_provider_registrations WHERE user_id=$1 AND provider_id=$2`, userID, provider.ID).Scan(&result.State, &result.ObservedAt, &result.Reason)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (db *Database) UnregisterSDKProvider(ctx context.Context, userID, providerID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		a, _, err := providerAuthorityTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !cap.ValidProviderID(providerID) || strings.Split(providerID, "/")[0] != a.AppID {
			return ErrAppRuntimeForbidden
		}
		_, err = tx.ExecContext(ctx, `UPDATE sdk_provider_registrations SET enabled=FALSE,reported_state='unavailable',reason='Provider unregistered.',observed_at=NOW(),updated_at=NOW() WHERE user_id=$1 AND provider_id=$2 AND app_id=$3`, userID, providerID, a.AppID)
		return err
	})
}

func (db *Database) ReportSDKProviderAvailability(ctx context.Context, userID, providerID string, state cap.Availability) error {
	if err := state.Validate(time.Now()); err != nil {
		return err
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		a, _, err := providerAuthorityTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !cap.ValidProviderID(providerID) || strings.Split(providerID, "/")[0] != a.AppID {
			return ErrAppRuntimeForbidden
		}
		result, err := tx.ExecContext(ctx, `UPDATE sdk_provider_registrations r SET reported_state=$4,observed_at=$5,reason=$6,updated_at=NOW() FROM user_app_installations i
   WHERE r.user_id=$1 AND r.provider_id=$2 AND r.app_id=$3 AND r.enabled AND r.observed_at<=$5 AND i.user_id=r.user_id AND i.app_id=r.app_id AND i.state='installed' AND i.installed_version=r.app_version AND i.installed_at=r.installed_at`, userID, providerID, a.AppID, state.State, state.ObservedAt, state.Reason)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrSDKProviderUnavailable
		}
		return nil
	})
}

type SDKProviderDiscovery struct {
	Capability string `json:"capability,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit"`
}
type SDKProviderPage struct {
	Providers  []cap.Provider `json:"providers"`
	NextCursor *string        `json:"nextCursor"`
}

func (db *Database) DiscoverSDKProviders(ctx context.Context, userID string, request SDKProviderDiscovery) (*SDKProviderPage, error) {
	if request.Limit == 0 {
		request.Limit = 50
	}
	if request.Limit < 1 || request.Limit > 100 || (request.Capability != "" && !cap.ValidName(request.Capability)) || len(request.Cursor) > 2048 {
		return nil, cap.ErrInvalid
	}
	var after string
	if request.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil || !cap.ValidProviderID(string(raw)) {
			return nil, cap.ErrInvalid
		}
		after = string(raw)
	}
	a := AppAuthorityFromContext(ctx)
	if a != nil {
		if err := db.ValidateAppExecutionAuthority(ctx, a, userID, a.SpaceID, "capabilities.read"); err != nil {
			return nil, err
		}
	}
	page := &SDKProviderPage{Providers: []cap.Provider{}}
	if request.TargetID != "" {
		if request.Cursor != "" {
			return nil, cap.ErrInvalid
		}
		provider, err := db.discoverSDKTargetProvider(ctx, userID, request.TargetID, request.Capability)
		if err != nil {
			return nil, err
		}
		if provider != nil {
			page.Providers = append(page.Providers, *provider)
		}
		return page, nil
	}

	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var callerScopes []string
		if a != nil {
			var raw []byte
			if err := tx.QueryRowContext(ctx, `SELECT granted_scopes FROM user_app_installations WHERE user_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, userID, a.AppID).Scan(&raw); err != nil {
				return err
			}
			if json.Unmarshal(raw, &callerScopes) != nil {
				return ErrAppRuntimeForbidden
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT v.definition,i.granted_scopes FROM sdk_provider_registrations r JOIN sdk_provider_versions v ON v.user_id=r.user_id AND v.provider_id=r.provider_id AND v.version=r.version
   JOIN user_app_installations i ON i.user_id=r.user_id AND i.app_id=r.app_id
   WHERE r.user_id=$1 AND r.enabled AND r.reported_state<>'revoked' AND i.state='installed' AND i.installed_version=r.app_version AND i.installed_at=r.installed_at AND r.provider_id>$2 ORDER BY r.provider_id`, userID, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw, grantRaw []byte
			if err := rows.Scan(&raw, &grantRaw); err != nil {
				return err
			}
			var provider cap.Provider
			var grants []string
			if json.Unmarshal(raw, &provider) != nil || json.Unmarshal(grantRaw, &grants) != nil {
				return ErrSpaceInvalid
			}
			allowed := []cap.Definition{}
			for _, definition := range provider.Capabilities {
				if request.Capability != "" && definition.Name != request.Capability {
					continue
				}
				authorized := true
				for _, scope := range definition.RequiredScopes {
					if !slices.Contains(grants, scope) || (a != nil && (!slices.Contains(callerScopes, scope) || !slices.Contains(a.Scopes, scope))) {
						authorized = false
						break
					}
				}
				if authorized {
					allowed = append(allowed, definition)
				}
			}
			if len(allowed) == 0 {
				continue
			}
			if len(page.Providers) == request.Limit {
				cursor := base64.RawURLEncoding.EncodeToString([]byte(page.Providers[len(page.Providers)-1].ID))
				page.NextCursor = &cursor
				break
			}
			provider.Capabilities = allowed
			page.Providers = append(page.Providers, provider)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}
