package integration

import (
	"context"
	"testing"

	"github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func newTestStripeService(database *db.Database) *billing.StripeService {
	return billing.NewStripeService(database, billing.WithChargeIDFetcher(func(paymentIntentID string) (string, error) {
		return "ch_" + paymentIntentID, nil
	}))
}

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

type waitlistEmailCall struct {
	recipientName  string
	recipientEmail string
	notifyEmail    string
	waitlistName   string
	waitlistEmail  string
}

type fakeWaitlistSender struct {
	confirmationCalls []waitlistEmailCall
	notificationCalls []waitlistEmailCall
	confirmationErr   error
	notificationErr   error
}

func (s *fakeWaitlistSender) SendWaitlistConfirmationEmail(_ context.Context, recipientName, recipientEmail string) error {
	s.confirmationCalls = append(s.confirmationCalls, waitlistEmailCall{
		recipientName:  recipientName,
		recipientEmail: recipientEmail,
	})
	return s.confirmationErr
}

func (s *fakeWaitlistSender) SendWaitlistNotificationEmail(_ context.Context, notifyEmail, waitlistName, waitlistEmail string) error {
	s.notificationCalls = append(s.notificationCalls, waitlistEmailCall{
		notifyEmail:   notifyEmail,
		waitlistName:  waitlistName,
		waitlistEmail: waitlistEmail,
	})
	return s.notificationErr
}

func countWaitlistSignups(t *testing.T, database *db.Database) int {
	t.Helper()

	var count int
	err := database.Conn.QueryRow(`SELECT COUNT(*) FROM waitlist_signups`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count waitlist signups: %v", err)
	}
	return count
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
