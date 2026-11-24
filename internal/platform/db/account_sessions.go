package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/misty-org/misty-server/internal/platform/security"
)

type AccountSession struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Current    bool      `json:"current"`
}

func (db *Database) AccountSessions(ctx context.Context, userID, currentHash string) ([]AccountSession, error) {
	out := []AccountSession{}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT token_hash,name,created_at,last_used_at FROM sessions
			WHERE user_id=$1 AND expires_at>now() ORDER BY last_used_at DESC, created_at DESC`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hash string
			var session AccountSession
			if err := rows.Scan(&hash, &session.Name, &session.CreatedAt, &session.LastUsedAt); err != nil {
				return err
			}
			session.ID = security.SessionPublicIDFromHash(hash)
			session.Current = currentHash != "" && hash == currentHash
			out = append(out, session)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) SignOutAccountSession(ctx context.Context, userID, sessionID string) (bool, error) {
	found := false
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT token_hash FROM sessions WHERE user_id=$1 FOR UPDATE`, userID)
		if err != nil {
			return err
		}
		target := ""
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				rows.Close()
				return err
			}
			if security.SessionPublicIDFromHash(hash) == sessionID {
				target = hash
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if target == "" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=$1 AND user_id=$2`, target, userID); err != nil {
			return err
		}
		found = true
		return signalCollabSessionEndedTx(ctx, tx, sessionID)
	})
	return found, err
}
