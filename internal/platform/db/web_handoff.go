package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) CreateWebHandoff(ctx context.Context, userID, hash string) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM web_handoff_codes WHERE expires_at<=NOW()`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO web_handoff_codes(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '2 minutes')`, hash, userID)
		return err
	})
}

func (db *Database) ConsumeWebHandoff(ctx context.Context, hash string) (string, error) {
	var userID string
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `DELETE FROM web_handoff_codes WHERE token_hash=$1 AND expires_at>NOW() RETURNING user_id`, hash).Scan(&userID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return userID, err
}
