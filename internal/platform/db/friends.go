package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/avatars"
)

var (
	ErrFriendRequestLimit = errors.New("friend request limit reached")
	ErrNotFriends         = errors.New("not friends")
)

type Person struct {
	UserID           string             `json:"user_id"`
	Username         string             `json:"username"`
	AvatarVersion    int64              `json:"avatar_version"`
	AvatarDecoration avatars.Decoration `json:"avatar_decoration"`
	BannerVersion    int64              `json:"banner_version"`
	Since            time.Time          `json:"since"`
}

type FriendsList struct {
	Friends  []Person `json:"friends"`
	Incoming []Person `json:"incoming"`
	Outgoing []Person `json:"outgoing"`
	Blocked  []Person `json:"blocked"`
}

type FriendPrivacy struct {
	FriendRequests string `json:"friend_requests"`
}

func friendPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func areFriendsTx(ctx context.Context, tx *sql.Tx, a, b string) (bool, error) {
	low, high := friendPair(a, b)
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM friendships WHERE user_low=$1 AND user_high=$2 AND state='accepted')`, low, high).Scan(&ok)
	return ok, err
}

func blockedEitherWayTx(ctx context.Context, tx *sql.Tx, a, b string) (bool, error) {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_blocks WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))`, a, b).Scan(&blocked)
	return blocked, err
}

func activeUserIDTx(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND lifecycle_state='active')`, userID).Scan(&ok)
	return ok, err
}

func (db *Database) Friends(ctx context.Context, userID string) (*FriendsList, error) {
	out := &FriendsList{Friends: []Person{}, Incoming: []Person{}, Outgoing: []Person{}, Blocked: []Person{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.avatar_version,u.avatar_decoration,u.banner_version,f.state,f.requested_by,COALESCE(f.accepted_at,f.created_at)
			FROM friendships f
			JOIN users u ON u.id = CASE WHEN f.user_low=$1 THEN f.user_high ELSE f.user_low END
			WHERE (f.user_low=$1 OR f.user_high=$1) AND u.lifecycle_state='active'
			ORDER BY lower(u.username)`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var person Person
			var state, requestedBy string
			if err := rows.Scan(&person.UserID, &person.Username, &person.AvatarVersion, &person.AvatarDecoration, &person.BannerVersion, &state, &requestedBy, &person.Since); err != nil {
				rows.Close()
				return err
			}
			switch {
			case state == "accepted":
				out.Friends = append(out.Friends, person)
			case requestedBy == userID:
				out.Outgoing = append(out.Outgoing, person)
			default:
				out.Incoming = append(out.Incoming, person)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		blocked, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.avatar_version,u.avatar_decoration,u.banner_version,b.created_at
			FROM user_blocks b JOIN users u ON u.id=b.blocked_id
			WHERE b.blocker_id=$1 ORDER BY lower(u.username)`, userID)
		if err != nil {
			return err
		}
		defer blocked.Close()
		for blocked.Next() {
			var person Person
			if err := blocked.Scan(&person.UserID, &person.Username, &person.AvatarVersion, &person.AvatarDecoration, &person.BannerVersion, &person.Since); err != nil {
				return err
			}
			out.Blocked = append(out.Blocked, person)
		}
		return blocked.Err()
	})
	return out, err
}

func (db *Database) SendFriendRequest(ctx context.Context, userID, username string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || len(username) > 30 {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "friends:send:"+userID); err != nil {
			return err
		}
		var limit *int64
		if err := asServiceTx(ctx, tx, func() error {
			limits, _, err := accountControlsTx(ctx, tx, userID)
			limit = limits.FriendRequestsPerDay
			return err
		}); err != nil {
			return err
		}
		if limit != nil {
			var sent int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM friend_request_sends WHERE user_id=$1 AND sent_at > now() - interval '24 hours'`, userID).Scan(&sent); err != nil {
				return err
			}
			if sent >= *limit {
				return ErrFriendRequestLimit
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO friend_request_sends(user_id) VALUES($1)`, userID); err != nil {
			return err
		}
		var otherID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(username)=$1 AND lifecycle_state='active'`, username).Scan(&otherID)
		if errors.Is(err, sql.ErrNoRows) || otherID == userID {
			return nil
		}
		if err != nil {
			return err
		}
		if blocked, err := blockedEitherWayTx(ctx, tx, userID, otherID); err != nil || blocked {
			return err
		}
		var setting string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT friend_requests FROM user_privacy WHERE user_id=$1),'everyone')`, otherID).Scan(&setting); err != nil {
			return err
		}
		if setting == "shared_spaces" {
			if shares, err := sharesSpaceTx(ctx, tx, userID, otherID); err != nil || !shares {
				return err
			}
		}
		low, high := friendPair(userID, otherID)
		var state, requestedBy string
		err = tx.QueryRowContext(ctx, `SELECT state,requested_by FROM friendships WHERE user_low=$1 AND user_high=$2 FOR UPDATE`, low, high).Scan(&state, &requestedBy)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO friendships(user_low,user_high,state,requested_by) VALUES($1,$2,'pending',$3)`, low, high, userID)
			return err
		case err != nil:
			return err
		case state == "pending" && requestedBy == otherID:
			_, err = tx.ExecContext(ctx, `UPDATE friendships SET state='accepted',accepted_at=now() WHERE user_low=$1 AND user_high=$2`, low, high)
			return err
		default:
			return nil
		}
	})
}

func (db *Database) AcceptFriendRequest(ctx context.Context, userID, otherID string) error {
	low, high := friendPair(userID, otherID)
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE friendships SET state='accepted',accepted_at=now()
			WHERE user_low=$1 AND user_high=$2 AND state='pending' AND requested_by=$3`, low, high, otherID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) RemoveFriendRequest(ctx context.Context, userID, otherID string) error {
	low, high := friendPair(userID, otherID)
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM friendships WHERE user_low=$1 AND user_high=$2 AND state='pending'`, low, high)
		return err
	})
}

func (db *Database) ClearIncomingFriendRequests(ctx context.Context, userID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM friendships WHERE state='pending' AND requested_by<>$1 AND (user_low=$1 OR user_high=$1)`, userID)
		return err
	})
}

func (db *Database) Unfriend(ctx context.Context, userID, otherID string) error {
	low, high := friendPair(userID, otherID)
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM friendships WHERE user_low=$1 AND user_high=$2 AND state='accepted'`, low, high)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		return deletePairDirectTx(ctx, tx, userID, otherID)
	})
}

func (db *Database) Block(ctx context.Context, userID, otherID string) error {
	if userID == otherID {
		return ErrSpaceInvalid
	}
	low, high := friendPair(userID, otherID)
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if ok, err := activeUserIDTx(ctx, tx, otherID); err != nil {
			return err
		} else if !ok {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_blocks(blocker_id,blocked_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, otherID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM friendships WHERE user_low=$1 AND user_high=$2`, low, high); err != nil {
			return err
		}
		return deletePairDirectTx(ctx, tx, userID, otherID)
	})
}

func (db *Database) Unblock(ctx context.Context, userID, otherID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM user_blocks WHERE blocker_id=$1 AND blocked_id=$2`, userID, otherID)
		return err
	})
}

func (db *Database) Privacy(ctx context.Context, userID string) (*FriendPrivacy, error) {
	out := &FriendPrivacy{FriendRequests: "everyone"}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT friend_requests FROM user_privacy WHERE user_id=$1`, userID).Scan(&out.FriendRequests)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return out, err
}

func (db *Database) SetPrivacy(ctx context.Context, userID string, privacy FriendPrivacy) (*FriendPrivacy, error) {
	if privacy.FriendRequests != "everyone" && privacy.FriendRequests != "shared_spaces" {
		return nil, ErrSpaceInvalid
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_privacy(user_id,friend_requests) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET friend_requests=EXCLUDED.friend_requests,updated_at=now()`, userID, privacy.FriendRequests)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &privacy, nil
}
