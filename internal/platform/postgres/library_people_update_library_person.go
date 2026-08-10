package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func (db *Database) UpdateLibraryPerson(ctx context.Context, userID, spaceID, personID string, version int64, name, coverItemID string) (*LibraryPerson, error) {
	name, coverItemID = strings.TrimSpace(name), strings.TrimSpace(coverItemID)
	if version < 1 || len([]rune(name)) > 120 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if coverItemID != "" {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_person_observations o JOIN space_people p ON p.id=o.person_id JOIN space_library_items i ON i.id=o.space_library_item_id WHERE p.id=$1 AND p.space_id=$2 AND o.space_library_item_id=$3 AND i.lifecycle_state='ready' AND i.hidden=FALSE)`, personID, spaceID, coverItemID).Scan(&valid); err != nil || !valid {
				return ErrLibraryInvalid
			}
		}
		var cover any
		if coverItemID != "" {
			cover = coverItemID
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_people SET name=$1,cover_item_id=$2,version=version+1,updated_at=NOW() WHERE id=$3 AND space_id=$4 AND version=$5 AND lifecycle_state='active'`, name, cover, personID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.people.updated", "person", personID, "success", map[string]any{})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPerson(ctx, userID, spaceID, personID)
}

func (db *Database) AddLibraryPersonItems(ctx context.Context, userID, spaceID, personID string, itemIDs []string) (*LibraryPerson, error) {
	itemIDs = uniqueSpaceIDs(itemIDs)
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		if err := addLibraryPersonItemsTx(ctx, tx, userID, spaceID, personID, itemIDs); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_people SET version=version+1,updated_at=NOW() WHERE id=$1`, personID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPerson(ctx, userID, spaceID, personID)
}

func (db *Database) RemoveLibraryPersonItems(ctx context.Context, userID, spaceID, personID string, itemIDs []string) (*LibraryPerson, error) {
	itemIDs = uniqueSpaceIDs(itemIDs)
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_person_observations o USING space_people p WHERE o.person_id=p.id AND p.id=$1 AND p.space_id=$2 AND o.space_library_item_id=ANY($3)`, personID, spaceID, pq.Array(itemIDs))
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryNotFound
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_people SET cover_item_id=CASE WHEN cover_item_id=ANY($2) THEN NULL ELSE cover_item_id END,version=version+1,updated_at=NOW() WHERE id=$1`, personID, pq.Array(itemIDs))
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPerson(ctx, userID, spaceID, personID)
}

func (db *Database) LibraryPersonItems(ctx context.Context, userID, spaceID, personID string, limit int) ([]SpaceLibraryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 200
	}
	items := []SpaceLibraryItem{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, libraryItemSelect+` JOIN space_person_observations o ON o.space_library_item_id=i.id JOIN space_people p ON p.id=o.person_id WHERE p.id=$1 AND p.space_id=$2 AND p.lifecycle_state='active' AND i.lifecycle_state='ready' AND i.hidden=FALSE GROUP BY i.id,f.id ORDER BY i.added_at DESC LIMIT $3`, personID, spaceID, limit)
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
	return items, err
}

func (db *Database) MergeLibraryPeople(ctx context.Context, userID, spaceID, sourceID, targetID string, sourceVersion, targetVersion int64) (*LibraryPerson, error) {
	if sourceID == targetID || sourceVersion < 1 || targetVersion < 1 {
		return nil, ErrLibraryInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var sourceKind, targetKind string
		var actualSourceVersion, actualTargetVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT kind,version FROM space_people WHERE id=$1 AND space_id=$2 AND lifecycle_state='active' FOR UPDATE`, sourceID, spaceID).Scan(&sourceKind, &actualSourceVersion); err != nil {
			return ErrLibraryNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT kind,version FROM space_people WHERE id=$1 AND space_id=$2 AND lifecycle_state='active' FOR UPDATE`, targetID, spaceID).Scan(&targetKind, &actualTargetVersion); err != nil {
			return ErrLibraryNotFound
		}
		if sourceKind != targetKind || actualSourceVersion != sourceVersion || actualTargetVersion != targetVersion {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_person_observations source USING space_person_observations target WHERE source.person_id=$1 AND target.person_id=$2 AND source.space_library_item_id=target.space_library_item_id AND source.derivative_id IS NOT DISTINCT FROM target.derivative_id`, sourceID, targetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_person_observations SET person_id=$1 WHERE person_id=$2`, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_people SET lifecycle_state='merged',merged_into_id=$1,cover_item_id=NULL,version=version+1,updated_at=NOW() WHERE id=$2`, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_people SET version=version+1,updated_at=NOW() WHERE id=$1`, targetID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.people.merged", sourceKind, targetID, "success", map[string]any{"source_id": sourceID})
	})
	if err != nil {
		return nil, err
	}
	return db.LibraryPerson(ctx, userID, spaceID, targetID)
}

func (db *Database) DeleteLibraryPerson(ctx context.Context, userID, spaceID, personID string, version int64) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_people SET lifecycle_state='deleted',cover_item_id=NULL,version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND version=$3 AND lifecycle_state='active'`, personID, spaceID, version)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_person_observations WHERE person_id=$1`, personID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.people.deleted", "person", personID, "success", map[string]any{})
	})
}

func requirePeopleKindEnabledTx(ctx context.Context, tx *sql.Tx, spaceID, kind string) error {
	var faces, pets bool
	if err := tx.QueryRowContext(ctx, `SELECT faces_enabled,pets_enabled FROM space_library_intelligence_policies WHERE space_id=$1`, spaceID).Scan(&faces, &pets); errors.Is(err, sql.ErrNoRows) {
		return ErrLibraryForbidden
	} else if err != nil {
		return err
	}
	if kind == "person" && !faces || kind == "pet" && !pets {
		return ErrLibraryForbidden
	}
	return nil
}

func addLibraryPersonItemsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, personID string, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM space_people WHERE id=$1 AND space_id=$2 AND lifecycle_state='active'`, personID, spaceID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return ErrLibraryNotFound
	} else if err != nil {
		return err
	}
	if err := requirePeopleKindEnabledTx(ctx, tx, spaceID, kind); err != nil {
		return err
	}
	var validCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_library_items i JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id WHERE i.space_id=$1 AND i.id=ANY($2) AND i.lifecycle_state='ready' AND i.hidden=FALSE AND b.server_detected_mime_type LIKE 'image/%'`, spaceID, pq.Array(itemIDs)).Scan(&validCount); err != nil {
		return err
	}
	if validCount != len(itemIDs) {
		return ErrLibraryInvalid
	}
	for _, itemID := range itemIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_person_observations(id,person_id,space_library_item_id,derivative_id,confidence,bounds,source) VALUES($1,$2,$3,NULL,1,'{}','manual') ON CONFLICT DO NOTHING`, "observation_"+uuid.NewString(), personID, itemID); err != nil {
			return err
		}
	}
	return nil
}

const libraryPersonSelect = `SELECT p.id,p.space_id,p.kind,p.name,COALESCE(CASE WHEN EXISTS(SELECT 1 FROM space_library_items cover WHERE cover.id=p.cover_item_id AND cover.lifecycle_state='ready' AND cover.hidden=FALSE) THEN p.cover_item_id END,''),(SELECT count(DISTINCT o.space_library_item_id) FROM space_person_observations o JOIN space_library_items i ON i.id=o.space_library_item_id WHERE o.person_id=p.id AND i.lifecycle_state='ready' AND i.hidden=FALSE),p.version,p.created_at,p.updated_at,p.lifecycle_state,COALESCE(p.merged_into_id,'') FROM space_people p`

func scanLibraryPerson(scanner interface{ Scan(...any) error }, person *LibraryPerson) error {
	return scanner.Scan(&person.ID, &person.SpaceID, &person.Kind, &person.Name, &person.CoverItemID, &person.ItemCount, &person.Version, &person.CreatedAt, &person.UpdatedAt, &person.Lifecycle, &person.MergedIntoID)
}
