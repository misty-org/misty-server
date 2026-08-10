package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func (db *Database) VerifyUserPassword(ctx context.Context, userID, password string) (bool, error) {
	if userID == "" || password == "" || len(password) > 1024 {
		return false, nil
	}
	var hash string
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}

func (db *Database) CreateLibraryReauthenticationGrant(ctx context.Context, userID, spaceID, scope, tokenHash string, lifetime time.Duration) (time.Time, error) {
	scope = strings.TrimSpace(scope)
	if !map[string]bool{"hidden": true, "recently_deleted": true, "bulk_export": true}[scope] || tokenHash == "" || lifetime < time.Minute || lifetime > 15*time.Minute {
		return time.Time{}, ErrLibraryInvalid
	}
	expiresAt := time.Now().Add(lifetime).UTC()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_reauthentication_grants WHERE expires_at<=NOW() OR (user_id=$1 AND space_id=$2 AND scope=$3)`, userID, spaceID, scope); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_reauthentication_grants(id,user_id,space_id,scope,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, "reauth_"+uuid.NewString(), userID, spaceID, scope, tokenHash, expiresAt); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.sensitive.reauthenticated", "reauthentication_grant", "", "success", map[string]any{"scope": scope})
	})
	return expiresAt, err
}

func (db *Database) ValidateLibraryReauthenticationGrant(ctx context.Context, userID, spaceID, scope, tokenHash string) error {
	if tokenHash == "" {
		return ErrLibraryReauthentication
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_reauthentication_grants WHERE user_id=$1 AND space_id=$2 AND scope=$3 AND token_hash=$4 AND expires_at>NOW())`, userID, spaceID, scope, tokenHash).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrLibraryReauthentication
		}
		return nil
	})
}

func (db *Database) RecordLibraryReauthenticationDenied(ctx context.Context, userID, spaceID, scope string) error {
	if !map[string]bool{"hidden": true, "recently_deleted": true, "bulk_export": true}[scope] {
		scope = "invalid"
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "library.sensitive.reauthentication_denied", "reauthentication_grant", "", "denied", map[string]any{"scope": scope})
	})
}

func (db *Database) SensitiveLibraryItemScope(ctx context.Context, userID, spaceID, itemID string) (string, error) {
	scope := ""
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var hidden bool
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT hidden,lifecycle_state FROM space_library_items WHERE id=$1 AND space_id=$2`, itemID, spaceID).Scan(&hidden, &state); err != nil {
			return err
		}
		if state == "trash" {
			scope = "recently_deleted"
		} else if hidden {
			scope = "hidden"
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryNotFound
	}
	return scope, err
}

func (db *Database) SensitiveLibraryAssetStackScope(ctx context.Context, userID, spaceID, stackID string) (string, error) {
	scope := ""
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		var trashed, hidden bool
		if err := tx.QueryRowContext(ctx, `SELECT bool_or(i.lifecycle_state='trash'),bool_or(i.hidden) FROM space_library_asset_stacks s JOIN space_library_asset_stack_members m ON m.stack_id=s.id JOIN space_library_items i ON i.id=m.space_library_item_id WHERE s.id=$1 AND s.space_id=$2 GROUP BY s.id`, stackID, spaceID).Scan(&trashed, &hidden); err != nil {
			return err
		}
		if trashed {
			scope = "recently_deleted"
		} else if hidden {
			scope = "hidden"
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryNotFound
	}
	return scope, err
}
