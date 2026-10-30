package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// VoiceLiveSession is one live transcription ticket.
type VoiceLiveSession struct {
	ID        string
	User      string
	Device    string
	Channels  []string
	MaxMS     int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (db *Database) CreateVoiceLiveSession(ctx context.Context, session VoiceLiveSession) error {
	if session.ID == "" || session.User == "" || session.Device == "" || session.MaxMS <= 0 {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(session.User), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO voice_live_sessions(id,owner_user_id,device_id,channels,max_ms,expires_at)
 VALUES($1,$2,$3,$4,$5,$6)`, session.ID, session.User, session.Device, pq.Array(session.Channels), session.MaxMS, session.ExpiresAt)
		return err
	})
}

// OpenVoiceLiveSession is a live session this account was issued that has not
// expired. ErrSpaceNotFound otherwise.
func (db *Database) OpenVoiceLiveSession(ctx context.Context, user, id string) (VoiceLiveSession, error) {
	var session VoiceLiveSession
	if user == "" || id == "" {
		return session, ErrSpaceNotFound
	}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,owner_user_id,device_id,channels,max_ms,created_at,expires_at
 FROM voice_live_sessions WHERE id::text=$1 AND owner_user_id=$2 AND expires_at>now()`, id, user).Scan(
			&session.ID, &session.User, &session.Device, pq.Array(&session.Channels), &session.MaxMS, &session.CreatedAt, &session.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return session, ErrSpaceNotFound
	}
	return session, err
}
