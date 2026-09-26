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
	appVersion := "builtin-1"
	var installed time.Time
	appID := strings.Split(provider.ID, "/")[0]
	err := tx.QueryRowContext(ctx, `SELECT created_at FROM users WHERE id=$1`, userID).Scan(&installed)
	if errors.Is(err, sql.ErrNoRows) {
		return provider, appVersion, installed, ErrSDKProviderUnavailable
	}
	if err != nil {
		return provider, appVersion, installed, err
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
