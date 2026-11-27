package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/avatars"
)

func SpaceAvatarObjectKey(spaceID string) string { return "avatars/space-" + spaceID }

const nextAvatarVersion = `GREATEST(avatar_version+1,(EXTRACT(EPOCH FROM clock_timestamp())*1000)::bigint)`

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

func requireSpacePictureAuthorityTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	var kind, pairKey string
	err := tx.QueryRowContext(ctx, `SELECT kind,COALESCE(direct_key,'') FROM spaces WHERE id=$1 AND lifecycle_state='active'`, spaceID).Scan(&kind, &pairKey)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSpaceNotFound
	}
	if err != nil {
		return err
	}
	if kind != "direct" {
		return requireSpaceAuthorityTx(ctx, tx, spaceID, userID, PermissionSpaceRename)
	}
	if pairKey != "" {
		return ErrSpaceInvalid
	}
	_, err = requireSpaceMemberTx(ctx, tx, spaceID, userID)
	return err
}

func (db *Database) RequireSpacePictureAuthority(ctx context.Context, userID, spaceID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return requireSpacePictureAuthorityTx(ctx, tx, spaceID, userID)
	})
}

func (db *Database) SetSpaceAvatar(ctx context.Context, userID, spaceID string, picture bool) (int64, error) {
	var version int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePictureAuthorityTx(ctx, tx, spaceID, userID); err != nil {
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

func (db *Database) SetSpaceAvatarDecoration(ctx context.Context, userID, spaceID string, decoration avatars.Decoration) (avatars.Decoration, error) {
	var out avatars.Decoration
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePictureAuthorityTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`UPDATE spaces SET avatar_decoration=$2,updated_at=NOW() WHERE id=$1 AND lifecycle_state='active' RETURNING avatar_decoration`,
			spaceID, decoration).Scan(&out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "space.updated", spaceID,
			map[string]any{"avatar_decoration": out})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return avatars.Decoration{}, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) AnnounceMemberAvatar(ctx context.Context, userID string, version int64) error {
	return db.announceMemberUpdate(ctx, userID, map[string]any{"user_id": userID, "avatar_version": version})
}

func (db *Database) AnnounceMemberAvatarDecoration(ctx context.Context, userID string, decoration avatars.Decoration) error {
	return db.announceMemberUpdate(ctx, userID, map[string]any{"user_id": userID, "avatar_decoration": decoration})
}

func (db *Database) announceMemberUpdate(ctx context.Context, userID string, payload map[string]any) error {
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
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.updated", userID, payload); err != nil {
				return err
			}
		}
		return nil
	})
}
