package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// SpaceEventCandidateUsers narrows fanout to current Space members. This is only
// a candidate set: EventByIDForUser must still enforce resource and conversation ACLs.
func (db *Database) SpaceEventCandidateUsers(ctx context.Context, eventID int64) ([]string, error) {
	users := []string{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT m.user_id FROM space_members m
   JOIN space_events e ON e.space_id=m.space_id WHERE e.id=$1`, eventID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user string
			if err := rows.Scan(&user); err != nil {
				return err
			}
			users = append(users, user)
		}
		return rows.Err()
	})
	return users, err
}

// SpaceEventForUsers loads an event once and returns the subset of users who
// may see it, applying the same membership and per-resource visibility rules
// as EventByIDForUser, in one transaction rather than one per recipient.
func (db *Database) SpaceEventForUsers(ctx context.Context, eventID int64, users []string) (*SpaceEvent, []string, error) {
	out := &SpaceEvent{}
	visible := []string{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT e.id,e.space_id,e.event_type,COALESCE(e.actor_user_id,''),COALESCE(e.entity_id,''),e.payload,e.created_at
			FROM space_events e WHERE e.id=$1`, eventID).Scan(&out.ID, &out.SpaceID, &out.EventType, &out.ActorUserID, &out.EntityID, &out.Payload, &out.CreatedAt); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1 AND user_id=ANY($2) ORDER BY user_id`, out.SpaceID, pq.Array(users))
		if err != nil {
			return err
		}
		var members []string
		for rows.Next() {
			var user string
			if err := rows.Scan(&user); err != nil {
				rows.Close()
				return err
			}
			members = append(members, user)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, user := range members {
			ok, err := spaceEventVisibleToUserTx(ctx, tx, user, *out, map[string]bool{})
			if err != nil {
				return err
			}
			if ok {
				visible = append(visible, user)
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSpaceNotFound
	}
	return out, visible, err
}
