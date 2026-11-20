package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ChosenStatus is what a person set: "online" (worked out from their connections, Idle included),
// "dnd" or "invisible", and an optional custom line with an emoji and when it ends.
type ChosenStatus struct {
	Chosen          string     `json:"chosen"`
	CustomText      string     `json:"custom_text,omitempty"`
	CustomEmoji     string     `json:"custom_emoji,omitempty"`
	CustomExpiresAt *time.Time `json:"custom_expires_at,omitempty"`
}

// live drops a custom line that has run out.
func (s ChosenStatus) live(now time.Time) ChosenStatus {
	if s.CustomExpiresAt != nil && !s.CustomExpiresAt.After(now) {
		s.CustomText, s.CustomEmoji, s.CustomExpiresAt = "", "", nil
	}
	return s
}

func chosenStatusTx(ctx context.Context, tx *sql.Tx, userID string) (ChosenStatus, error) {
	out := ChosenStatus{Chosen: "online"}
	var text, emoji sql.NullString
	var expires sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT chosen,custom_text,custom_emoji,custom_expires_at FROM user_status WHERE user_id=$1`, userID).Scan(&out.Chosen, &text, &emoji, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.CustomText, out.CustomEmoji = text.String, emoji.String
	if expires.Valid {
		out.CustomExpiresAt = &expires.Time
	}
	return out.live(time.Now()), nil
}

// ChosenStatuses returns what each of these people chose; anyone who never chose is Online.
func (db *Database) ChosenStatuses(ctx context.Context, userIDs []string) (map[string]ChosenStatus, error) {
	out := map[string]ChosenStatus{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		for _, id := range uniqueSpaceIDs(userIDs) {
			status, err := chosenStatusTx(ctx, tx, id)
			if err != nil {
				return err
			}
			out[id] = status
		}
		return nil
	})
	return out, err
}

// SetChosenStatus saves the person's status. A custom line is trimmed, at most 128 characters.
func (db *Database) SetChosenStatus(ctx context.Context, userID string, status ChosenStatus) (*ChosenStatus, error) {
	if status.Chosen != "online" && status.Chosen != "dnd" && status.Chosen != "invisible" {
		return nil, ErrSpaceInvalid
	}
	status.CustomText = strings.TrimSpace(status.CustomText)
	status.CustomEmoji = strings.TrimSpace(status.CustomEmoji)
	if utf8.RuneCountInString(status.CustomText) > 128 || utf8.RuneCountInString(status.CustomEmoji) > 32 {
		return nil, ErrSpaceInvalid
	}
	if status.CustomText == "" && status.CustomEmoji == "" {
		status.CustomExpiresAt = nil
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_status(user_id,chosen,custom_text,custom_emoji,custom_expires_at) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5)
			ON CONFLICT(user_id) DO UPDATE SET chosen=EXCLUDED.chosen,custom_text=EXCLUDED.custom_text,custom_emoji=EXCLUDED.custom_emoji,
			custom_expires_at=EXCLUDED.custom_expires_at,updated_at=now()`, userID, status.Chosen, status.CustomText, status.CustomEmoji, status.CustomExpiresAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	live := status.live(time.Now())
	return &live, nil
}

// StatusAudience is who may see the person's status: their friends and everyone they share a
// Space with, less anyone either of them blocked.
func (db *Database) StatusAudience(ctx context.Context, userID string) ([]string, error) {
	out := []string{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT other FROM (
				SELECT CASE WHEN user_low=$1 THEN user_high ELSE user_low END AS other
				FROM friendships WHERE state='accepted' AND (user_low=$1 OR user_high=$1)
				UNION
				SELECT b.user_id FROM space_members a
				JOIN space_members b ON b.space_id=a.space_id AND b.user_id<>a.user_id
				JOIN spaces s ON s.id=a.space_id AND s.lifecycle_state='active' AND NOT s.is_default
				WHERE a.user_id=$1
			) people
			WHERE NOT EXISTS(SELECT 1 FROM user_blocks WHERE (blocker_id=$1 AND blocked_id=other) OR (blocker_id=other AND blocked_id=$1))`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}

// AccountNotice is something the server tells a person once.
type AccountNotice struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// AccountNotices returns the notices the person hasn't seen.
func (db *Database) AccountNotices(ctx context.Context, userID string) ([]AccountNotice, error) {
	out := []AccountNotice{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,kind,payload,created_at FROM account_notices WHERE user_id=$1 AND seen_at IS NULL ORDER BY created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var notice AccountNotice
			var raw []byte
			if err := rows.Scan(&notice.ID, &notice.Kind, &raw, &notice.CreatedAt); err != nil {
				return err
			}
			notice.Payload = json.RawMessage(raw)
			out = append(out, notice)
		}
		return rows.Err()
	})
	return out, err
}

// MarkAccountNoticeSeen keeps a notice from showing again.
func (db *Database) MarkAccountNoticeSeen(ctx context.Context, userID, noticeID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE account_notices SET seen_at=now() WHERE id=$1 AND user_id=$2 AND seen_at IS NULL`, noticeID, userID)
		return err
	})
}

// CanJoinCall says whether the person may join a call room. A room is "dm:<dm id>", open to that
// DM's people while the DM may take messages; Space voice channels ("space:<id>:voice:<channel>")
// are not open yet.
func (db *Database) CanJoinCall(ctx context.Context, userID, room string) (bool, error) {
	if !strings.HasPrefix(room, "dm:") {
		return false, nil
	}
	spaceID := strings.TrimPrefix(room, "dm:")
	allowed := false
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := requireDirectMemberTx(ctx, tx, userID, spaceID); err != nil {
			if errors.Is(err, ErrSpaceNotFound) {
				return nil
			}
			return err
		}
		if err := requireDirectWritableTx(ctx, tx, userID, spaceID); err != nil {
			if errors.Is(err, ErrNotFriends) {
				return nil
			}
			return err
		}
		allowed = true
		return nil
	})
	return allowed, err
}

// DirectMemberIDs lists the people in a DM the caller is in, for ringing a call.
func (db *Database) DirectMemberIDs(ctx context.Context, userID, spaceID string) ([]string, error) {
	out := []string{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := requireDirectMemberTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}
