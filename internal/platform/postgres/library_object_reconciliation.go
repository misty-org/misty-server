package db

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// LibraryObjectExpectation is the immutable metadata PostgreSQL expects for an
// R2 object. Source distinguishes permanent blobs from uploads that may still
// be finalized by a retrying client.
type LibraryObjectExpectation struct {
	ObjectKey string
	ByteSize  int64
	SHA256    string
	Source    string
}

// LibraryObjectExpectations resolves a bounded inventory page in one query.
// Terminal failed/expired uploads are deliberately excluded so their old
// objects become eligible for orphan cleanup after the safety window.
func (db *Database) LibraryObjectExpectations(
	ctx context.Context, objectKeys []string,
) (map[string]LibraryObjectExpectation, error) {
	out := make(map[string]LibraryObjectExpectation, len(objectKeys))
	if len(objectKeys) == 0 {
		return out, nil
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
			// A ready blob is authoritative when the upload row that created it
			// is also present.
			if existing, ok := out[item.ObjectKey]; !ok || item.Source == "blob" ||
				existing.Source != "blob" {
				out[item.ObjectKey] = item
			}
		}
		return rows.Err()
	})
	return out, err
}

// LibraryReadyBlobExpectations returns permanent objects that should exist.
// Reconciliation HEADs this bounded set to surface missing data without
// automatically destroying database references that may still be recoverable.
func (db *Database) LibraryReadyBlobExpectations(
	ctx context.Context, limit int,
) ([]LibraryObjectExpectation, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	out := make([]LibraryObjectExpectation, 0, limit)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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

// LibraryInterruptedFinalizations returns uploaded objects whose database
// transaction has not completed yet. These remain client-retryable until their
// reservation expires; the reconciler reports them instead of guessing the
// parent record or bypassing validation.
func (db *Database) LibraryInterruptedFinalizations(
	ctx context.Context, limit int,
) ([]LibraryObjectExpectation, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	out := make([]LibraryObjectExpectation, 0, limit)
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
