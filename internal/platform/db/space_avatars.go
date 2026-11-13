package db

import (
	"context"
	"database/sql"
)

// SpaceAvatarObjectKey is where a Space's picture lives in the object store: under the avatars/
// prefix the bucket's lifecycle rules already keep, beside people's. One flat name, since the
// store takes no nested keys (`libraryObjectKeyPattern`).
func SpaceAvatarObjectKey(spaceID string) string { return "avatars/space-" + spaceID }

// nextAvatarVersion is SQL for a picture's next version: one past the current, and never below
// the clock in milliseconds. A removed picture's version drops to 0, so a counter would hand the
// next upload a number an earlier picture already had and every app holding that one would keep
// showing it; the clock never repeats one.
const nextAvatarVersion = `GREATEST(avatar_version+1,(EXTRACT(EPOCH FROM clock_timestamp())*1000)::bigint)`

// SpaceAvatarVersion is the version of a Space's picture for one of its members (0: none).
func (db *Database) SpaceAvatarVersion(ctx context.Context, userID, spaceID string) (int64, error) {
	var version int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx,
			`SELECT avatar_version FROM spaces WHERE id=$1 AND lifecycle_state='active'`,
			spaceID).Scan(&version)
	})
	if err == sql.ErrNoRows {
		return 0, ErrSpaceNotFound
	}
	return version, err
}

// RequireSpaceOwner checks that the user owns the Space, before its picture is stored.
func (db *Database) RequireSpaceOwner(ctx context.Context, userID, spaceID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return requireSpaceOwnerTx(ctx, tx, spaceID, userID)
	})
}

// SetSpaceAvatar records a new picture (picture true) or its removal, for the owner only, and
// tells the Space's members so their apps show it. It returns the new version (0 once removed).
func (db *Database) SetSpaceAvatar(ctx context.Context, userID, spaceID string, picture bool) (int64, error) {
	var version int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		query := `UPDATE spaces SET avatar_version=0,updated_at=NOW() WHERE id=$1 RETURNING avatar_version`
		if picture {
			query = `UPDATE spaces SET avatar_version=` + nextAvatarVersion + `,updated_at=NOW() WHERE id=$1 RETURNING avatar_version`
		}
		if err := tx.QueryRowContext(ctx, query, spaceID).Scan(&version); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "space.updated", spaceID,
			map[string]any{"avatar_version": version})
		return err
	})
	return version, err
}

// AnnounceMemberAvatar tells every Space the person is in that their picture changed (version 0:
// removed), so the other members' apps show the new one without a reload.
func (db *Database) AnnounceMemberAvatar(ctx context.Context, userID string, version int64) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT m.space_id FROM space_members m JOIN spaces s ON s.id=m.space_id
			WHERE m.user_id=$1 AND s.lifecycle_state='active'`, userID)
		if err != nil {
			return err
		}
		spaces := []string{}
		for rows.Next() {
			var spaceID string
			if err := rows.Scan(&spaceID); err != nil {
				rows.Close()
				return err
			}
			spaces = append(spaces, spaceID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, spaceID := range spaces {
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.updated", userID,
				map[string]any{"user_id": userID, "avatar_version": version}); err != nil {
				return err
			}
		}
		return nil
	})
}
