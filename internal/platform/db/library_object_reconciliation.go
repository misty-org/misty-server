package db

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

type LibraryObjectExpectation struct {
	ObjectKey string
	ByteSize  int64
	SHA256    string
	Source    string
}

func (db *Database) LibraryObjectExpectations(
	ctx context.Context, objectKeys []string,
) (map[string]LibraryObjectExpectation, error) {
	out := make(map[string]LibraryObjectExpectation, len(objectKeys))
	if len(objectKeys) == 0 {
		return out, nil
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT r2_object_key,byte_size,sha256,'blob'
			FROM library_blobs
			WHERE r2_object_key=ANY($1) AND lifecycle_state<>'deleted'
			UNION ALL
			SELECT object_key,requested_byte_size,client_sha256,'upload'
			FROM space_library_uploads
			WHERE object_key=ANY($1)
			  AND state IN (
			      'initiated','uploading','uploaded_unverified','quarantined',
			      'scanning','processing','ready'
			  )`,
			pq.Array(objectKeys),
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LibraryObjectExpectation
			if err := rows.Scan(
				&item.ObjectKey, &item.ByteSize, &item.SHA256, &item.Source,
			); err != nil {
				return err
			}
			if existing, ok := out[item.ObjectKey]; !ok || item.Source == "blob" ||
				existing.Source != "blob" {
				out[item.ObjectKey] = item
			}
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) LibraryReadyBlobExpectations(
	ctx context.Context, limit int,
) ([]LibraryObjectExpectation, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	out := make([]LibraryObjectExpectation, 0, limit)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT r2_object_key,byte_size,sha256
			FROM library_blobs
			WHERE lifecycle_state='ready'
			ORDER BY updated_at,r2_object_key
			LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item := LibraryObjectExpectation{Source: "blob"}
			if err := rows.Scan(&item.ObjectKey, &item.ByteSize, &item.SHA256); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) LibraryInterruptedFinalizations(
	ctx context.Context, limit int,
) ([]LibraryObjectExpectation, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	out := make([]LibraryObjectExpectation, 0, limit)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT object_key,requested_byte_size,client_sha256
			FROM space_library_uploads
			WHERE state='uploaded_unverified' AND expires_at>NOW()
			ORDER BY updated_at,object_key
			LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item := LibraryObjectExpectation{Source: "upload"}
			if err := rows.Scan(&item.ObjectKey, &item.ByteSize, &item.SHA256); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
