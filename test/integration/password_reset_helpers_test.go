package integration

import (
	"context"
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type passwordResetEmailCall struct {
	recipientEmail string
	resetLink      string
}

type fakePasswordResetSender struct {
	calls []passwordResetEmailCall
	err   error
}

func (s *fakePasswordResetSender) SendPasswordResetEmail(_ context.Context, recipientEmail, resetLink string) error {
	s.calls = append(s.calls, passwordResetEmailCall{
		recipientEmail: recipientEmail,
		resetLink:      resetLink,
	})
	return s.err
}

func getStoredPasswordResetTokenHash(t *testing.T, database *db.Database, userID string) string {
	t.Helper()

	var tokenHash string
	err := database.Conn.QueryRow(
		`SELECT hashed_token FROM password_reset_tokens WHERE user_id = $1`,
		userID,
	).Scan(&tokenHash)
	if err != nil {
		t.Fatalf("failed to fetch password reset token hash: %v", err)
	}
	return tokenHash
}
