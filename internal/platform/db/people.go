package db

import (
	"context"
	"database/sql"
	"errors"
)

type SharedSpace struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AvatarVersion int64  `json:"avatar_version"`
}

func requireOtherPersonTx(ctx context.Context, tx *sql.Tx, viewerID, otherID string) error {
	if viewerID == "" || otherID == "" || viewerID == otherID {
		return ErrSpaceNotFound
	}
	if ok, err := activeUserIDTx(ctx, tx, otherID); err != nil {
		return err
	} else if !ok {
		return ErrSpaceNotFound
	}
	return nil
}

func (db *Database) PersonAvatarReference(ctx context.Context, otherID string) (UserAvatarReference, error) {
	var avatar UserAvatarReference
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT avatar_version,COALESCE(avatar_object_key,'avatars/'||id) FROM users WHERE id=$1 AND lifecycle_state='active'`, otherID).Scan(&avatar.Version, &avatar.ObjectKey)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	return avatar, err
}

func (db *Database) PersonBannerReference(ctx context.Context, viewerID, otherID string) (UserBannerReference, error) {
	var banner UserBannerReference
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireOtherPersonTx(ctx, tx, viewerID, otherID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT banner_version,COALESCE(banner_object_key,'banners/'||id) FROM users WHERE id=$1`, otherID).Scan(&banner.Version, &banner.ObjectKey)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		return err
	})
	return banner, err
}

func (db *Database) MutualFriends(ctx context.Context, viewerID, otherID string) ([]Person, error) {
	out := []Person{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireOtherPersonTx(ctx, tx, viewerID, otherID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.avatar_version,u.banner_version,COALESCE(fa.accepted_at,fa.created_at)
			FROM friendships fa
			JOIN users u ON u.id = CASE WHEN fa.user_low=$1 THEN fa.user_high ELSE fa.user_low END
			WHERE (fa.user_low=$1 OR fa.user_high=$1) AND fa.state='accepted'
				AND u.lifecycle_state='active' AND u.id<>$2
				AND EXISTS(SELECT 1 FROM friendships fb WHERE fb.state='accepted'
					AND ((fb.user_low=u.id AND fb.user_high=$2) OR (fb.user_low=$2 AND fb.user_high=u.id)))
			ORDER BY lower(u.username),u.id`, viewerID, otherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var person Person
			if err := rows.Scan(&person.UserID, &person.Username, &person.AvatarVersion, &person.BannerVersion, &person.Since); err != nil {
				return err
			}
			out = append(out, person)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) SharedSpaces(ctx context.Context, viewerID, otherID string) ([]SharedSpace, error) {
	out := []SharedSpace{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireOtherPersonTx(ctx, tx, viewerID, otherID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name,s.avatar_version FROM spaces s
			JOIN space_members a ON a.space_id=s.id AND a.user_id=$1
			JOIN space_members b ON b.space_id=s.id AND b.user_id=$2
			WHERE s.lifecycle_state='active' AND s.kind='space' AND NOT s.is_default
			ORDER BY lower(s.name),s.id`, viewerID, otherID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var space SharedSpace
			if err := rows.Scan(&space.ID, &space.Name, &space.AvatarVersion); err != nil {
				return err
			}
			out = append(out, space)
		}
		return rows.Err()
	})
	return out, err
}
