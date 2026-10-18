package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type CreateSpaceResult struct {
	Space Space `json:"space"`
}

// CreateSpace makes an empty Space owned by the caller. Every Space starts
// the same; what people and agents make in it is what makes it different.
// A repeated idempotency key returns the Space the first request created.
func (db *Database) CreateSpace(ctx context.Context, userID, name, idempotencyKey string) (*CreateSpaceResult, error) {
	name, err := normalizeSpaceName(name)
	if err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) > 200 {
		return nil, ErrSpaceInvalid
	}
	result := &CreateSpaceResult{Space: Space{
		ID: "space_" + uuid.NewString(), SecurityDomainID: "sd_" + uuid.NewString(),
		OwnerUserID: userID, Name: name, Role: "owner", MemberCount: 1,
	}}
	fingerprintDigest := sha256.Sum256([]byte(name))
	fingerprint := hex.EncodeToString(fingerprintDigest[:])
	existingSpaceID := ""
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if idempotencyKey != "" {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:create:"+userID+":"+idempotencyKey); err != nil {
				return err
			}
			var storedFingerprint string
			err := tx.QueryRowContext(ctx, `SELECT request_fingerprint,space_id FROM space_creation_requests WHERE user_id=$1 AND idempotency_key=$2`,
				userID, idempotencyKey).Scan(&storedFingerprint, &existingSpaceID)
			if err == nil {
				if storedFingerprint != fingerprint {
					return ErrSpaceConflict
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:owner:"+userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)`, result.Space.SecurityDomainID, userID, result.Space.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
			SELECT $1,$2,$3,$4,NOT EXISTS(
				SELECT 1 FROM spaces WHERE owner_user_id=$2 AND is_default AND lifecycle_state='active'
			)
			RETURNING is_default,created_at,updated_at`, result.Space.ID, userID, name, result.Space.SecurityDomainID).Scan(&result.Space.IsDefault, &result.Space.CreatedAt, &result.Space.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1)`, result.Space.ID); err != nil {
			return err
		}
		if err := addSpaceMembershipTx(ctx, tx, result.Space.ID, userID, "owner"); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, result.Space.ID, userID, "space.created", result.Space.ID, map[string]any{"name": name}); err != nil {
			return err
		}
		if idempotencyKey != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO space_creation_requests(user_id,idempotency_key,request_fingerprint,space_id) VALUES($1,$2,$3,$4)`,
				userID, idempotencyKey, fingerprint, result.Space.ID)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if existingSpaceID != "" {
		space, err := db.SpaceByID(ctx, userID, existingSpaceID)
		if err != nil {
			return nil, err
		}
		return &CreateSpaceResult{Space: *space}, nil
	}
	return result, nil
}

// SpacePerson is someone the caller already shares an active Space with.
type SpacePerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// SpacePeople lists the people the caller shares active Spaces with, most
// recently joined first, so a new Space can invite them in one click.
func (db *Database) SpacePeople(ctx context.Context, userID string) ([]SpacePerson, error) {
	people := []SpacePerson{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.email
			FROM space_members mine
			JOIN spaces s ON s.id=mine.space_id AND s.lifecycle_state='active'
			JOIN space_members other ON other.space_id=mine.space_id AND other.user_id<>mine.user_id
			JOIN users u ON u.id=other.user_id
			WHERE mine.user_id=$1
			GROUP BY u.id,u.username,u.email
			ORDER BY max(other.joined_at) DESC, u.id
			LIMIT 24`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var person SpacePerson
			if err := rows.Scan(&person.UserID, &person.Name, &person.Email); err != nil {
				return err
			}
			people = append(people, person)
		}
		return rows.Err()
	})
	return people, err
}
