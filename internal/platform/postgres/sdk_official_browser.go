package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	"strings"
	"time"
)

func sdkOfficialBrowserProviderTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, provider cap.Provider) (cap.Provider, string, time.Time, error) {
	var appVersion string
	var installed time.Time
	var rawScopes []byte
	appID := strings.Split(provider.ID, "/")[0]
	err := tx.QueryRowContext(ctx, `SELECT installed_version,installed_at,granted_scopes FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, spaceID, appID).Scan(&appVersion, &installed, &rawScopes)
	if errors.Is(err, sql.ErrNoRows) {
		return provider, appVersion, installed, ErrSDKProviderUnavailable
	}
	if err != nil {
		return provider, appVersion, installed, err
	}
	var scopes []string
	if json.Unmarshal(rawScopes, &scopes) != nil || !cap.HasScopes(scopes, []string{"browser.inspect"}) {
		return provider, appVersion, installed, ErrAppRuntimeForbidden
	}
	expected, _ := json.Marshal(provider)
	_, err = tx.ExecContext(ctx, `INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, userID, provider.ID, provider.Version, appID, expected)
	if err != nil {
		return provider, appVersion, installed, err
	}
	var actual []byte
	err = tx.QueryRowContext(ctx, `SELECT definition FROM sdk_provider_versions WHERE user_id=$1 AND provider_id=$2 AND version=$3`, userID, provider.ID, provider.Version).Scan(&actual)
	if err == nil && !cap.EqualJSON(expected, actual) {
		err = ErrSDKVersionConflict
	}
	return provider, appVersion, installed, err
}
