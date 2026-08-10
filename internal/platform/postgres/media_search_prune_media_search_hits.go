package db

import (
	"context"
	"database/sql"
	"errors"
)

func pruneMediaSearchHits(hits []MediaSearchHit) []MediaSearchHit {
	hasLexical := false
	topSemantic := 0.0
	for _, hit := range hits {
		if hit.LexicalScore > 0 {
			hasLexical = true
		}
		if hit.SemanticScore > topSemantic {
			topSemantic = hit.SemanticScore
		}
	}
	if !hasLexical {
		return hits
	}
	minimum := max(0.72, topSemantic-0.08)
	filtered := make([]MediaSearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.LexicalScore > 0 || hit.SemanticScore >= minimum {
			filtered = append(filtered, hit)
		}
	}
	return filtered
}

func (db *Database) MediaSearchAsset(userID, deviceID, assetID string) (*MediaSearchAsset, error) {
	var a MediaSearchAsset
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT user_id,device_id,asset_id,fingerprint,media_type,mime_type,duration_ms,status,indexed_through_ms FROM media_search_assets WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, userID, deviceID, assetID).Scan(&a.UserID, &a.DeviceID, &a.AssetID, &a.Fingerprint, &a.MediaType, &a.MimeType, &a.DurationMS, &a.Status, &a.IndexedThroughMS)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

func (db *Database) MediaSearchAssets(userID, deviceID string) ([]MediaSearchAsset, error) {
	assets := []MediaSearchAsset{}
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT user_id,device_id,asset_id,fingerprint,media_type,mime_type,duration_ms,status,indexed_through_ms FROM media_search_assets WHERE user_id=$1 AND device_id=$2 ORDER BY updated_at DESC`, userID, deviceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a MediaSearchAsset
			if err = rows.Scan(&a.UserID, &a.DeviceID, &a.AssetID, &a.Fingerprint, &a.MediaType, &a.MimeType, &a.DurationMS, &a.Status, &a.IndexedThroughMS); err != nil {
				return err
			}
			assets = append(assets, a)
		}
		return rows.Err()
	})
	return assets, err
}

func (db *Database) DeleteMediaSearchAsset(userID, deviceID, assetID string) (bool, error) {
	deleted := false
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.Exec(`DELETE FROM media_search_assets WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, userID, deviceID, assetID)
		if err == nil {
			count, _ := result.RowsAffected()
			deleted = count == 1
		}
		return err
	})
	return deleted, err
}

func (db *Database) DeleteMediaSearchDevice(userID, deviceID string) (bool, error) {
	deleted := false
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.Exec(`DELETE FROM media_search_devices WHERE user_id=$1 AND device_id=$2`, userID, deviceID)
		if err == nil {
			count, _ := result.RowsAffected()
			deleted = count == 1
		}
		return err
	})
	return deleted, err
}

// AdoptLegacyMediaSearchDevice atomically assigns the pre-device-scoping
// catalog to one real device. Only the first upgraded device can adopt it;
// later devices receive ready=false and must explicitly re-index local media.
func (db *Database) AdoptLegacyMediaSearchDevice(userID, deviceID string) (ready, adopted bool, err error) {
	err = db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM media_search_devices WHERE user_id=$1 AND device_id=$2)`, userID, deviceID).Scan(&ready); err != nil {
			return err
		}
		if ready {
			return nil
		}
		var legacy string
		queryErr := tx.QueryRow(`SELECT device_id FROM media_search_devices WHERE user_id=$1 AND device_id=$2 FOR UPDATE`, userID, LegacyMediaSearchDeviceID).Scan(&legacy)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return nil
		}
		if queryErr != nil {
			return queryErr
		}
		if _, err := tx.Exec(`INSERT INTO media_search_devices(user_id,device_id,created_at,last_seen_at) SELECT user_id,$2,created_at,NOW() FROM media_search_devices WHERE user_id=$1 AND device_id=$3`, userID, deviceID, LegacyMediaSearchDeviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO media_search_assets(user_id,device_id,asset_id,fingerprint,media_type,mime_type,duration_ms,status,indexed_through_ms,created_at,updated_at) SELECT user_id,$2,asset_id,fingerprint,media_type,mime_type,duration_ms,status,indexed_through_ms,created_at,updated_at FROM media_search_assets WHERE user_id=$1 AND device_id=$3`, userID, deviceID, LegacyMediaSearchDeviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO media_search_chunks(user_id,device_id,asset_id,chunk_index,fingerprint,start_ms,end_ms,status,failure_code,created_at,updated_at) SELECT user_id,$2,asset_id,chunk_index,fingerprint,start_ms,end_ms,status,failure_code,created_at,updated_at FROM media_search_chunks WHERE user_id=$1 AND device_id=$3`, userID, deviceID, LegacyMediaSearchDeviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE media_search_segments SET device_id=$2 WHERE user_id=$1 AND device_id=$3`, userID, deviceID, LegacyMediaSearchDeviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM media_search_devices WHERE user_id=$1 AND device_id=$2`, userID, LegacyMediaSearchDeviceID); err != nil {
			return err
		}
		ready, adopted = true, true
		return nil
	})
	return ready, adopted, err
}
