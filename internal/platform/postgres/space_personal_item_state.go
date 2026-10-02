package db

import (
	"context"
	"database/sql"
	"regexp"
	"time"
)

type SpacePersonalItemState struct {
	ItemKey  string     `json:"item_key"`
	Favorite bool       `json:"favorite"`
	OpenedAt *time.Time `json:"opened_at,omitempty"`
}

var spaceItemKeyPattern = regexp.MustCompile(`^(note|drawing|task|file|chat):[A-Za-z0-9_-]{1,128}$`)

func ValidSpaceItemKey(key string) bool { return spaceItemKeyPattern.MatchString(key) }

// These are private references, not grants of access to the referenced content.
// Clients resolve them through the resource's existing permission-checked reads.
func (db *Database) SpacePersonalItems(ctx context.Context, userID, spaceID string) ([]SpacePersonalItemState, error) {
	out := []SpacePersonalItemState{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT item_key,favorite,opened_at FROM space_personal_item_state WHERE user_id=$1 AND space_id=$2 ORDER BY opened_at DESC NULLS LAST,item_key`, userID, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpacePersonalItemState
			if err := rows.Scan(&item.ItemKey, &item.Favorite, &item.OpenedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) UpdateSpacePersonalItem(ctx context.Context, userID, spaceID, key string, favorite *bool, opened bool) (SpacePersonalItemState, error) {
	var out SpacePersonalItemState
	if !ValidSpaceItemKey(key) || (favorite == nil && !opened) {
		return out, ErrSpaceInvalid
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		// Serialize this account/Space's small personal index, including retention.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, userID+":"+spaceID); err != nil {
			return err
		}
		if favorite != nil && *favorite {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_personal_item_state WHERE user_id=$1 AND space_id=$2 AND favorite AND item_key<>$3`, userID, spaceID, key).Scan(&count); err != nil {
				return err
			}
			if count >= 2000 {
				return ErrSpaceInvalid
			}
		}
		err := tx.QueryRowContext(ctx, `INSERT INTO space_personal_item_state(user_id,space_id,item_key,favorite,opened_at)
   VALUES($1,$2,$3,COALESCE($4::boolean,false),CASE WHEN $5 THEN now() ELSE NULL END)
   ON CONFLICT(user_id,space_id,item_key) DO UPDATE SET
   favorite=COALESCE($4::boolean,space_personal_item_state.favorite),
   opened_at=CASE WHEN $5 THEN now() ELSE space_personal_item_state.opened_at END
   RETURNING item_key,favorite,opened_at`, userID, spaceID, key, favorite, opened).Scan(&out.ItemKey, &out.Favorite, &out.OpenedAt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM space_personal_item_state WHERE user_id=$1 AND space_id=$2 AND NOT favorite AND item_key NOT IN
   (SELECT item_key FROM space_personal_item_state WHERE user_id=$1 AND space_id=$2 AND opened_at IS NOT NULL ORDER BY opened_at DESC,item_key LIMIT 100)`, userID, spaceID)
		return err
	})
	return out, err
}
