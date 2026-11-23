package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"
)

var ErrInviteCodeNotFound = errors.New("no such invite code")

func newInviteCode() (string, error) {
	raw := make([]byte, 7)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))[:10], nil
}

func (db *Database) InviteCode(ctx context.Context, userID string) (string, error) {
	generated, err := newInviteCode()
	if err != nil {
		return "", err
	}
	var code string
	err = db.serviceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO invite_codes(user_id,code) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING`, userID, generated); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT code FROM invite_codes WHERE user_id=$1`, userID).Scan(&code)
	})
	return code, err
}

func (db *Database) InviterName(ctx context.Context, code string) (string, error) {
	var name string
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT u.username FROM invite_codes c
			JOIN users u ON u.id=c.user_id
			LEFT JOIN account_controls a ON a.user_id=c.user_id
			WHERE c.code=$1 AND a.suspended_at IS NULL AND a.delete_at IS NULL`, code).Scan(&name)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInviteCodeNotFound
	}
	return name, err
}
