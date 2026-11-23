package db

import (
	"context"
	"database/sql"
	"regexp"
)

const maxCallKeysPerAccount = 10

var (
	callKeyIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	callPublicKeyPattern = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{32,256}$`)
)

type CallKey struct {
	UserID    string `json:"user_id"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

func (db *Database) PutCallKey(ctx context.Context, userID, keyID, publicKey string) error {
	if !callKeyIDPattern.MatchString(keyID) || !callPublicKeyPattern.MatchString(publicKey) {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO call_keys(user_id,key_id,public_key) VALUES($1,$2,$3)
			ON CONFLICT(user_id,key_id) DO UPDATE SET public_key=EXCLUDED.public_key,updated_at=now()`, userID, keyID, publicKey); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM call_keys WHERE user_id=$1 AND key_id NOT IN (
			SELECT key_id FROM call_keys WHERE user_id=$1 ORDER BY updated_at DESC LIMIT $2)`, userID, maxCallKeysPerAccount)
		return err
	})
}

func (db *Database) DirectCallKeys(ctx context.Context, userID, spaceID string) ([]CallKey, error) {
	out := []CallKey{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := requireDirectMemberTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT k.user_id,k.key_id,k.public_key FROM call_keys k
			JOIN space_members m ON m.user_id=k.user_id AND m.space_id=$1 ORDER BY k.user_id,k.updated_at DESC`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key CallKey
			if err := rows.Scan(&key.UserID, &key.KeyID, &key.PublicKey); err != nil {
				return err
			}
			out = append(out, key)
		}
		return rows.Err()
	})
	return out, err
}
