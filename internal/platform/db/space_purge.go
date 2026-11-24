package db

import (
	"context"
	"database/sql"
)

func (db *Database) DueSpacePurges(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	ids := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM spaces WHERE lifecycle_state='pending_deletion'
			AND permanent_delete_after<=NOW() ORDER BY permanent_delete_after LIMIT $1`, limit)
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
	return ids, err
}

func (db *Database) BeginSpacePurge(ctx context.Context, spaceID string) (int, error) {
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_notes SET lifecycle_state=$1,acl_version=acl_version+1,updated_at=NOW()
			WHERE space_id=$2 AND lifecycle_state<>$1`, NoteLifecycleDeleting, spaceID)
		return err
	})
	return 0, err
}

func spacePurgeIDsTx(ctx context.Context, tx *sql.Tx, query, spaceID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (db *Database) SpacePurgeObjectKeys(ctx context.Context, spaceID string) ([]string, error) {
	keys := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		keys, err = spacePurgeIDsTx(ctx, tx, `SELECT b.r2_object_key FROM library_blobs b JOIN spaces s ON s.security_domain_id=b.security_domain_id
			WHERE s.id=$1 AND s.lifecycle_state='pending_deletion'
			UNION SELECT u.object_key FROM space_library_uploads u JOIN spaces s ON s.id=u.space_id OR s.security_domain_id=u.security_domain_id
			WHERE s.id=$1 AND s.lifecycle_state='pending_deletion'
			UNION SELECT 'avatars/space-'||s.id FROM spaces s
			WHERE s.id=$1 AND s.lifecycle_state='pending_deletion' AND s.avatar_version>0`, spaceID)
		return err
	})
	return keys, err
}

func (db *Database) DeleteSpaceData(ctx context.Context, spaceID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		var domain string
		err := tx.QueryRowContext(ctx, `SELECT security_domain_id FROM spaces WHERE id=$1 AND lifecycle_state='pending_deletion' FOR UPDATE`, spaceID).Scan(&domain)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS security_domains_space_fk, spaces_security_domain_fk DEFERRED`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_drive_items WHERE space_id=$1`, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM spaces WHERE id=$1`, spaceID); err != nil {
			return err
		}
		for _, statement := range []string{
			`DELETE FROM library_exports WHERE security_domain_id=$1`,
			`DELETE FROM library_legal_holds WHERE security_domain_id=$1`,
			`DELETE FROM library_recovery_tombstones WHERE security_domain_id=$1`,
			`DELETE FROM library_derivatives WHERE security_domain_id=$1`,
			`DELETE FROM library_processing_jobs WHERE security_domain_id=$1`,
			`DELETE FROM space_library_uploads WHERE security_domain_id=$1`,
			`DELETE FROM library_files WHERE security_domain_id=$1`,
			`DELETE FROM library_blobs WHERE security_domain_id=$1`,
			`DELETE FROM security_domains WHERE id=$1`,
		} {
			if _, err := tx.ExecContext(ctx, statement, domain); err != nil {
				return err
			}
		}
		return nil
	})
}
