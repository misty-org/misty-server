package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
)

// ErrVoiceSessionClosed means the live session was already settled or released.
var ErrVoiceSessionClosed = errors.New("voice session is already closed")

// VoiceLiveSession is one live transcription ticket and what it used.
type VoiceLiveSession struct {
	ID          string
	User        string
	Device      string
	Channels    []string
	MaxMS       int64
	UsedMS      int64
	Reservation json.RawMessage
	Status      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

func (db *Database) CreateVoiceLiveSession(ctx context.Context, session VoiceLiveSession) error {
	if session.ID == "" || session.User == "" || session.Device == "" || session.MaxMS <= 0 || !json.Valid(session.Reservation) {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(session.User), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO voice_live_sessions(id,owner_user_id,device_id,channels,max_ms,reservation,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, session.ID, session.User, session.Device, pq.Array(session.Channels), session.MaxMS, []byte(session.Reservation), session.ExpiresAt)
		return err
	})
}

// CloseVoiceLiveSession records the audio a session used, capped at its
// ticket's limit, and returns the session as it was before closing. It closes
// once: a second call returns ErrVoiceSessionClosed with the closed session.
func (db *Database) CloseVoiceLiveSession(ctx context.Context, user, id string, usedMS int64, status string) (VoiceLiveSession, error) {
	var session VoiceLiveSession
	if user == "" || id == "" || usedMS < 0 || (status != "settled" && status != "released") {
		return session, ErrSpaceInvalid
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var reservation []byte
		if err := tx.QueryRowContext(ctx, `SELECT id,owner_user_id,device_id,channels,max_ms,used_ms,reservation,status,created_at,expires_at
 FROM voice_live_sessions WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, id, user).Scan(
			&session.ID, &session.User, &session.Device, pq.Array(&session.Channels), &session.MaxMS, &session.UsedMS, &reservation, &session.Status, &session.CreatedAt, &session.ExpiresAt); err != nil {
			return err
		}
		session.Reservation = reservation
		if session.Status != "open" {
			return ErrVoiceSessionClosed
		}
		used := min(usedMS, session.MaxMS)
		_, err := tx.ExecContext(ctx, `UPDATE voice_live_sessions SET used_ms=$3,status=$4,settled_at=now() WHERE id=$1 AND owner_user_id=$2`, id, user, used, status)
		session.UsedMS = used
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return session, ErrSpaceNotFound
	}
	return session, err
}

// VoiceLiveUsedSince sums the audio used by live sessions created since a
// time, counting open sessions at their full limit so concurrent tickets
// cannot overrun the allowance.
func (db *Database) VoiceLiveUsedSince(ctx context.Context, user string, since time.Time) (int64, error) {
	var total int64
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status='open' AND expires_at > now() THEN max_ms ELSE used_ms END),0)
 FROM voice_live_sessions WHERE owner_user_id=$1 AND created_at >= $2`, user, since).Scan(&total)
	})
	return total, err
}
