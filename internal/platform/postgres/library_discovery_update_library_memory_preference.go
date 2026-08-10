package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

func (db *Database) UpdateLibraryMemoryPreference(ctx context.Context, userID, spaceID, memoryID string, version int64, title, coverItemID, musicItemID string, playbackSeconds float64) error {
	title, coverItemID, musicItemID = strings.TrimSpace(title), strings.TrimSpace(coverItemID), strings.TrimSpace(musicItemID)
	if !libraryMemoryIDPattern.MatchString(memoryID) || version < 0 || len([]rune(title)) > 160 || playbackSeconds < 1 || playbackSeconds > 15 {
		return ErrLibraryInvalid
	}
	start, _ := time.Parse("2006-01", memoryID)
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if coverItemID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$3 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$4)`, coverItemID, spaceID, start, start.AddDate(0, 1, 0)).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
		}
		if musicItemID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.id=$1 AND i.space_id=$2 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND b.server_detected_mime_type LIKE 'audio/%')`, musicItemID, spaceID).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
		}
		var cover, music any
		if coverItemID != "" {
			cover = coverItemID
		}
		if musicItemID != "" {
			music = musicItemID
		}
		if version == 0 {
			result, err := tx.ExecContext(ctx, `INSERT INTO space_memory_preferences(space_id,memory_id,title,cover_item_id,music_item_id,playback_seconds,updated_by_user_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, spaceID, memoryID, title, cover, music, playbackSeconds, userID)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrLibraryConflict
			}
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE space_memory_preferences SET title=$1,cover_item_id=$2,music_item_id=$3,playback_seconds=$4,updated_by_user_id=$5,version=version+1,updated_at=NOW() WHERE space_id=$6 AND memory_id=$7 AND version=$8`, title, cover, music, playbackSeconds, userID, spaceID, memoryID, version)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrLibraryConflict
			}
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.memory.updated", "memory", memoryID, "success", map[string]any{"music": musicItemID != "", "playback_seconds": playbackSeconds})
	})
}

func (db *Database) LibraryDiscoveryItems(ctx context.Context, userID, spaceID, kind, groupID string) ([]SpaceLibraryItem, error) {
	if groupID == "" || (kind != "day" && kind != "month" && kind != "year" && kind != "memory" && kind != "trip" && kind != "duplicate" && kind != "map") {
		return nil, ErrLibraryInvalid
	}
	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		statement, args := "", []any{spaceID}
		switch kind {
		case "day":
			if !libraryDayIDPattern.MatchString(groupID) {
				return ErrLibraryInvalid
			}
			start, err := time.Parse("2006-01-02", groupID)
			if err != nil {
				return ErrLibraryInvalid
			}
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$2 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$3 ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, start, start.AddDate(0, 0, 1))
		case "month":
			if !libraryMemoryIDPattern.MatchString(groupID) {
				return ErrLibraryInvalid
			}
			start, err := time.Parse("2006-01", groupID)
			if err != nil {
				return ErrLibraryInvalid
			}
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$2 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$3 ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, start, start.AddDate(0, 1, 0))
		case "year":
			if !libraryYearIDPattern.MatchString(groupID) {
				return ErrLibraryInvalid
			}
			start, err := time.Parse("2006", groupID)
			if err != nil {
				return ErrLibraryInvalid
			}
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$2 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$3 ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, start, start.AddDate(1, 0, 0))
		case "memory":
			if !libraryMemoryIDPattern.MatchString(groupID) {
				return ErrLibraryInvalid
			}
			start, err := time.Parse("2006-01", groupID)
			if err != nil {
				return ErrLibraryInvalid
			}
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)>=$2 AND COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)<$3 ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, start, start.AddDate(0, 1, 0))
		case "trip":
			if len([]rune(groupID)) > 200 {
				return ErrLibraryInvalid
			}
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND lower(COALESCE(COALESCE(i.location_override,f.intrinsic_location)->>'name',''))=lower($2) ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, strings.TrimSpace(groupID))
		case "duplicate":
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE AND f.blob_id=(SELECT anchor_file.blob_id FROM space_library_items anchor JOIN library_files anchor_file ON anchor_file.id=anchor.file_id WHERE anchor.id=$2 AND anchor.space_id=$1 AND anchor.lifecycle_state='ready') ORDER BY i.added_at DESC,i.id`
			args = append(args, groupID)
		case "map":
			if !libraryMapIDPattern.MatchString(groupID) {
				return ErrLibraryInvalid
			}
			coordinates := strings.Split(groupID, ",")
			statement = libraryItemSelect + ` WHERE i.space_id=$1 AND i.lifecycle_state='ready' AND i.hidden=FALSE
				AND jsonb_typeof(COALESCE(i.location_override,f.intrinsic_location)->'latitude')='number' AND jsonb_typeof(COALESCE(i.location_override,f.intrinsic_location)->'longitude')='number'
				AND round((COALESCE(i.location_override,f.intrinsic_location)->>'latitude')::numeric,2)=round($2::numeric,2) AND round((COALESCE(i.location_override,f.intrinsic_location)->>'longitude')::numeric,2)=round($3::numeric,2)
				ORDER BY COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at) DESC,i.id`
			args = append(args, coordinates[0], coordinates[1])
		}
		args = append(args, userID)
		statement = strings.Replace(statement, " ORDER BY", " AND "+resourceAudienceSQL("i", fmt.Sprintf("$%d", len(args)))+" ORDER BY", 1)
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceLibraryItem
			if err := scanSpaceLibraryItem(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err == nil && len(items) == 0 {
		return nil, ErrLibraryNotFound
	}
	return items, err
}

func libraryDateRange(start, end time.Time) string {
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return start.Format("Jan 2, 2006")
	}
	return start.Format("Jan 2, 2006") + " – " + end.Format("Jan 2, 2006")
}

func (db *Database) MergeLibraryDuplicates(ctx context.Context, userID, spaceID string, keeper LibraryItemVersion, duplicates []LibraryItemVersion) (*SpaceLibraryItem, error) {
	all := append([]LibraryItemVersion{keeper}, duplicates...)
	if keeper.ID == "" || keeper.Version < 1 || len(duplicates) < 1 || len(duplicates) > 99 {
		return nil, ErrLibraryInvalid
	}
	versions, ids := map[string]int64{}, make([]string, 0, len(all))
	for _, item := range all {
		if item.ID == "" || item.Version < 1 || versions[item.ID] != 0 {
			return nil, ErrLibraryInvalid
		}
		versions[item.ID], ids = item.Version, append(ids, item.ID)
	}
	duplicateIDs := ids[1:]
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		for _, id := range ids {
			if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, id); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.version,i.lifecycle_state,f.blob_id FROM space_library_items i JOIN library_files f ON f.id=i.file_id WHERE i.space_id=$1 AND i.id=ANY($2) FOR UPDATE OF i`, spaceID, pq.Array(ids))
		if err != nil {
			return err
		}
		found, blobID := 0, ""
		for rows.Next() {
			var id, state, candidateBlobID string
			var version int64
			if err := rows.Scan(&id, &version, &state, &candidateBlobID); err != nil {
				_ = rows.Close()
				return err
			}
			if versions[id] != version || state != "ready" {
				_ = rows.Close()
				return ErrLibraryConflict
			}
			if blobID != "" && blobID != candidateBlobID {
				_ = rows.Close()
				return ErrLibraryInvalid
			}
			blobID, found = candidateBlobID, found+1
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if found != len(ids) {
			return ErrLibraryNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_items keeper SET
			favorite=(SELECT bool_or(candidate.favorite) FROM space_library_items candidate WHERE candidate.id=ANY($1)),
			tags=(SELECT COALESCE(jsonb_agg(tag ORDER BY lower(tag)),'[]'::jsonb) FROM (SELECT DISTINCT tag FROM space_library_items candidate CROSS JOIN LATERAL jsonb_array_elements_text(candidate.tags) tag WHERE candidate.id=ANY($1)) merged_tags),
			caption=COALESCE(NULLIF(keeper.caption,''),(SELECT NULLIF(candidate.caption,'') FROM space_library_items candidate WHERE candidate.id=ANY($1) AND candidate.caption<>'' ORDER BY candidate.added_at LIMIT 1),''),
			date_override=COALESCE(keeper.date_override,(SELECT min(candidate.date_override) FROM space_library_items candidate WHERE candidate.id=ANY($1))),
			location_override=COALESCE(keeper.location_override,(SELECT candidate.location_override FROM space_library_items candidate WHERE candidate.id=ANY($1) AND candidate.location_override IS NOT NULL ORDER BY candidate.added_at LIMIT 1)),
			version=keeper.version+1,updated_at=NOW() WHERE keeper.id=$2 AND keeper.space_id=$3`, pq.Array(ids), keeper.ID, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_album_items(album_id,space_library_item_id,added_by_user_id,position,added_at) SELECT album_id,$1,$2,min(position),min(added_at) FROM space_album_items WHERE space_library_item_id=ANY($3) GROUP BY album_id ON CONFLICT(album_id,space_library_item_id) DO NOTHING`, keeper.ID, userID, pq.Array(duplicateIDs)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_message_library_references(message_id,space_id,space_library_item_id,created_by_user_id,created_at) SELECT message_id,space_id,$1,created_by_user_id,min(created_at) FROM space_message_library_references WHERE space_library_item_id=ANY($2) GROUP BY message_id,space_id,created_by_user_id ON CONFLICT(message_id,space_library_item_id) DO NOTHING`, keeper.ID, pq.Array(duplicateIDs)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_person_observations(id,person_id,space_library_item_id,derivative_id,confidence,bounds,source,created_at) SELECT 'observation_'||replace(gen_random_uuid()::text,'-',''),person_id,$1,derivative_id,confidence,bounds,source,created_at FROM space_person_observations WHERE space_library_item_id=ANY($2) ON CONFLICT(person_id,space_library_item_id,derivative_id) DO NOTHING`, keeper.ID, pq.Array(duplicateIDs)); err != nil {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM space_album_items WHERE space_library_item_id=ANY($1)`,
			`DELETE FROM space_message_library_references WHERE space_library_item_id=ANY($1)`,
			`DELETE FROM space_person_observations WHERE space_library_item_id=ANY($1)`,
		} {
			if _, err := tx.ExecContext(ctx, statement, pq.Array(duplicateIDs)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_items SET lifecycle_state='trash',trashed_at=NOW(),recover_until=NOW()+INTERVAL '30 days',version=version+1,updated_at=NOW() WHERE space_id=$1 AND id=ANY($2)`, spaceID, pq.Array(duplicateIDs)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='recovery',updated_at=NOW() WHERE space_id=$1 AND source_kind='library_item' AND source_id=ANY($2) AND state='active'`, spaceID, pq.Array(duplicateIDs)); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.duplicates.merged", "library_item", keeper.ID, "success", map[string]any{"merged_count": len(duplicateIDs)})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryItem(ctx, userID, spaceID, keeper.ID)
}
