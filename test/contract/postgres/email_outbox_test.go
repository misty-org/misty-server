package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/email"
	"github.com/misty-org/misty-server/test/testkit"
)

// fakeMailjet stands in for Mailjet.
type fakeMailjet struct {
	sent     []email.Message
	failures int
}

func (f *fakeMailjet) Send(_ context.Context, m email.Message) error {
	if f.failures > 0 {
		f.failures--
		return &email.Error{Status: 503, Message: "unavailable"}
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailjet) subjects() []string {
	out := []string{}
	for _, m := range f.sent {
		out = append(out, m.Subject)
	}
	return out
}

type outboxRow struct {
	kind, state, reason string
	secret              bool
	attempts            int
}

func outboxRows(t *testing.T, database *Database, userID string) []outboxRow {
	t.Helper()
	rows, err := database.Conn.QueryContext(context.Background(), `SELECT kind,state,COALESCE(cancel_reason,''),setup_key IS NOT NULL OR (link IS NOT NULL AND kind IN ('setup','sign_in')),attempts
		FROM email_outbox WHERE user_id=$1 ORDER BY created_at,kind`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []outboxRow{}
	for rows.Next() {
		var row outboxRow
		if err := rows.Scan(&row.kind, &row.state, &row.reason, &row.secret, &row.attempts); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func outboxAccount(t *testing.T, database *Database) *User {
	t.Helper()
	user, _, err := database.CreateOperatorAccount(context.Background(), "outbox-"+uuid.NewString()+"@example.com", AccountLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func issueWithEmail(t *testing.T, database *Database, user *User, kind, key string) {
	t.Helper()
	message := &KeyEmail{Kind: kind, Recipient: user.Email, Key: key, Link: "https://mistysys.com/setup#server=api.example.com&key=" + key}
	if err := database.IssueSetupKeyWithEmail(context.Background(), user.ID, "hash-"+uuid.NewString(), time.Now().Add(time.Hour), message); err != nil {
		t.Fatal(err)
	}
}

func deliverAll(t *testing.T, database *Database, sender email.Sender) {
	t.Helper()
	if err := email.DeliverDue(context.Background(), database, sender); err != nil {
		t.Fatal(err)
	}
}

func TestASetupEmailIsSentOnceAndKeepsNoKey(t *testing.T) {
	database := testkit.OpenDatabase(t)
	user := outboxAccount(t, database)
	issueWithEmail(t, database, user, EmailSetup, "misty_setup_first")
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 || mail.sent[0].To != user.Email || !strings.Contains(mail.sent[0].Text, "misty_setup_first") {
		t.Fatalf("sent = %+v", mail.sent)
	}
	if rows := outboxRows(t, database, user.ID); len(rows) != 1 || rows[0].state != "sent" || rows[0].secret {
		t.Fatalf("outbox = %+v", rows)
	}
}

func TestAFailedSendBacksOffAndKeepsItsKeyUntilItGoes(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user := outboxAccount(t, database)
	issueWithEmail(t, database, user, EmailSetup, "misty_setup_retry")
	mail := &fakeMailjet{failures: 1}
	deliverAll(t, database, mail)
	if rows := outboxRows(t, database, user.ID); len(mail.sent) != 0 || rows[0].state != "pending" || !rows[0].secret || rows[0].attempts != 1 {
		t.Fatalf("after a failure: %v %+v", mail.subjects(), rows)
	}
	var code string
	if err := database.Conn.QueryRowContext(ctx, `SELECT last_error FROM email_outbox WHERE user_id=$1`, user.ID).Scan(&code); err != nil || code != "mailjet_503" {
		t.Fatalf("last error = %q %v", code, err)
	}
	deliverAll(t, database, mail) // Not due yet: it backs off.
	if len(mail.sent) != 0 {
		t.Fatal("retried before its backoff")
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE email_outbox SET send_after=now() WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	deliverAll(t, database, mail)
	if rows := outboxRows(t, database, user.ID); len(mail.sent) != 1 || rows[0].state != "sent" || rows[0].secret {
		t.Fatalf("after the retry: %v %+v", mail.subjects(), rows)
	}
}

func TestANewerKeyCancelsTheUnsentEmailOfTheOldOne(t *testing.T) {
	database := testkit.OpenDatabase(t)
	user := outboxAccount(t, database)
	issueWithEmail(t, database, user, EmailSetup, "misty_setup_old")
	issueWithEmail(t, database, user, EmailSignIn, "misty_setup_new")
	rows := outboxRows(t, database, user.ID)
	if len(rows) != 2 || rows[0].state != "cancelled" || rows[0].reason != "superseded" || rows[0].secret || rows[1].state != "pending" {
		t.Fatalf("outbox = %+v", rows)
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0].Text, "misty_setup_new") || strings.Contains(mail.sent[0].Text, "misty_setup_old") {
		t.Fatalf("sent = %+v", mail.sent)
	}
}

func TestAKeyAndItsEmailAreStoredTogether(t *testing.T) {
	database := testkit.OpenDatabase(t)
	user := outboxAccount(t, database)
	// A kind the table refuses fails the whole step, so no key is left without its email.
	bad := &KeyEmail{Kind: "nonsense", Recipient: user.Email, Key: "misty_setup_bad", Link: "https://mistysys.com/setup"}
	if err := database.IssueSetupKeyWithEmail(context.Background(), user.ID, "hash-"+uuid.NewString(), time.Now().Add(time.Hour), bad); err == nil {
		t.Fatal("a bad email was accepted")
	}
	state, err := database.AccountSetupState(context.Background(), user.ID)
	if err != nil || state.LiveKey {
		t.Fatalf("state = %+v %v: a key exists without its email", state, err)
	}
}

func TestClosingSendsANoticeAndAWarningThreeDaysBefore(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user := outboxAccount(t, database)
	deleteAt := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	keep := "https://example.com/keep"
	for range 2 { // A repeated close stores the pair once.
		if err := database.QueueClosingEmails(ctx, user.ID, user.Email, keep, deleteAt); err != nil {
			t.Fatal(err)
		}
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 || mail.sent[0].Subject != "Your Misty account is read-only" || !strings.Contains(mail.sent[0].Text, keep) {
		t.Fatalf("notice = %v", mail.subjects())
	}
	var due time.Time
	if err := database.Conn.QueryRowContext(ctx, `SELECT send_after FROM email_outbox WHERE user_id=$1 AND kind='deletion_warning' AND state='pending'`, user.ID).Scan(&due); err != nil || !due.Equal(deleteAt.Add(-3*24*time.Hour)) {
		t.Fatalf("warning due %v (deletion %v) %v", due, deleteAt, err)
	}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 {
		t.Fatalf("the warning went early: %v", mail.subjects())
	}
	for range 2 {
		if _, err := database.Conn.ExecContext(ctx, `UPDATE email_outbox SET send_after=now() WHERE user_id=$1 AND kind='deletion_warning' AND state='pending'`, user.ID); err != nil {
			t.Fatal(err)
		}
		deliverAll(t, database, mail)
	}
	if len(mail.sent) != 2 || !strings.Contains(mail.sent[1].Text, "deleted in 3 days") {
		t.Fatalf("emails = %v", mail.subjects())
	}
}

func TestANoticeSentLateStandsForTheWarning(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user := outboxAccount(t, database)
	deleteAt := time.Now().Add(2 * 24 * time.Hour)
	if err := database.QueueClosingEmails(ctx, user.ID, user.Email, "", deleteAt); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 {
		t.Fatalf("emails = %v", mail.subjects())
	}
	for _, row := range outboxRows(t, database, user.ID) {
		if row.kind == EmailDeletionWarning && (row.state != "cancelled" || row.reason != "notice_late") {
			t.Fatalf("warning = %+v", row)
		}
	}
}

func TestResumingCancelsPendingClosingEmails(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user := outboxAccount(t, database)
	deleteAt := time.Now().Add(30 * 24 * time.Hour)
	if err := database.QueueClosingEmails(ctx, user.ID, user.Email, "", deleteAt); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	if err := database.CancelClosingEmails(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE email_outbox SET send_after=now() WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 {
		t.Fatalf("emails = %v", mail.subjects())
	}
	// Closed again later: a new notice and warning for the new date.
	if err := database.QueueClosingEmails(ctx, user.ID, user.Email, "", deleteAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	deliverAll(t, database, mail)
	if len(mail.sent) != 2 {
		t.Fatalf("emails = %v", mail.subjects())
	}
}

func TestInvitationEmailsFollowTheInvitation(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	invitation := "invite-" + uuid.NewString()
	recipient := "invited-" + uuid.NewString() + "@example.com"
	if err := database.QueueInvitationEmail(ctx, invitation, recipient, "misty://open/invite/old", "Team", "Sam"); err != nil {
		t.Fatal(err)
	}
	// A new link replaces the old one, whose email had not gone.
	if err := database.QueueInvitationEmail(ctx, invitation, recipient, "misty://open/invite/new", "Team", "Sam"); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 || mail.sent[0].To != recipient || !strings.Contains(mail.sent[0].Text, "invite/new") || mail.sent[0].Subject != "Sam invited you to Team on Misty" {
		t.Fatalf("sent = %+v", mail.sent)
	}
	var kept int
	if err := database.Conn.QueryRowContext(ctx, `SELECT count(*) FROM email_outbox WHERE invitation_id=$1 AND link IS NOT NULL`, invitation).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("%d rows still hold a link: %v", kept, err)
	}
	// A revoked invitation's unsent email never goes.
	revoked := "invite-" + uuid.NewString()
	if err := database.QueueInvitationEmail(ctx, revoked, recipient, "misty://open/invite/revoked", "Team", "Sam"); err != nil {
		t.Fatal(err)
	}
	if err := database.CancelInvitationEmail(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	deliverAll(t, database, mail)
	if len(mail.sent) != 1 {
		t.Fatalf("a revoked invitation was emailed: %v", mail.subjects())
	}
}

func TestOldKeysAreDroppedAndFinishedRowsForgotten(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	user := outboxAccount(t, database)
	issueWithEmail(t, database, user, EmailSetup, "misty_setup_stale")
	if _, err := database.Conn.ExecContext(ctx, `UPDATE email_outbox SET created_at=now()-interval '8 days' WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMailjet{}
	deliverAll(t, database, mail)
	if rows := outboxRows(t, database, user.ID); len(mail.sent) != 0 || rows[0].state != "cancelled" || rows[0].reason != "expired" || rows[0].secret {
		t.Fatalf("stale key: %v %+v", mail.subjects(), rows)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE email_outbox SET updated_at=now()-interval '31 days' WHERE user_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	deliverAll(t, database, mail)
	if rows := outboxRows(t, database, user.ID); len(rows) != 0 {
		t.Fatalf("finished rows kept: %+v", rows)
	}
}

func TestSendErrorsNeverHideARow(t *testing.T) {
	database := testkit.OpenDatabase(t)
	user := outboxAccount(t, database)
	issueWithEmail(t, database, user, EmailSetup, "misty_setup_plainerror")
	if err := email.DeliverDue(context.Background(), database, failingSender{}); err != nil {
		t.Fatal(err)
	}
	if rows := outboxRows(t, database, user.ID); rows[0].state != "pending" || rows[0].attempts != 1 {
		t.Fatalf("outbox = %+v", rows)
	}
}

type failingSender struct{}

func (failingSender) Send(context.Context, email.Message) error { return errors.New("network down") }
