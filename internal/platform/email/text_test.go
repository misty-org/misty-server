package email

import (
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func TestKeyEmailsSayWhatToDo(t *testing.T) {
	link := "https://mistysys.com/setup#server=api.mistysys.com&key=misty_setup_abc"
	subject, text, body := keyEmail(false, link, "api.mistysys.com", "misty_setup_abc")
	for _, want := range []string{"on the computer where Misty is installed", link, "with the server api.mistysys.com", "misty_setup_abc", "works once and expires in 7 days"} {
		if !strings.Contains(text, want) {
			t.Fatalf("setup text lacks %q:\n%s", want, text)
		}
	}
	if subject != "Your Misty account is ready" || !strings.Contains(body, "server=api.mistysys.com&amp;key=misty_setup_abc") || strings.Contains(text, "new password") {
		t.Fatalf("setup = %q\n%s", subject, body)
	}
	if subject, text, _ = keyEmail(true, link, "api.mistysys.com", "misty_setup_abc"); subject != "Sign in to Misty" || !strings.Contains(text, "set a new password") || !strings.Contains(text, link) {
		t.Fatalf("sign-in = %q\n%s", subject, text)
	}
}

func TestConfirmEmailCarriesItsLink(t *testing.T) {
	link := "https://mistysys.com/confirm-email#server=api.mistysys.com&token=abc"
	message, ok := messageFor(db.OutgoingEmail{ID: "1", Kind: db.EmailConfirm, Recipient: "a@example.com", Link: link})
	if !ok || message.Subject != "Confirm your email for Misty" || !strings.Contains(message.Text, link) || !strings.Contains(message.HTML, "token=abc") || !strings.Contains(message.Text, "expires in 24 hours") {
		t.Fatalf("confirm email = %+v, %v", message, ok)
	}
	if _, ok := messageFor(db.OutgoingEmail{ID: "1", Kind: db.EmailConfirm, Recipient: "a@example.com"}); ok {
		t.Fatal("an email with no link went out")
	}
}

func TestClosingEmailsSayWhereToKeepTheAccount(t *testing.T) {
	deleteAt := time.Date(2026, 11, 30, 8, 0, 0, 0, time.FixedZone("x", 3600))
	keep := "https://mistysys.com/pricing"
	subject, text, body := closingEmail(false, deleteAt, keep)
	for _, want := range []string{"read-only", "until November 30, 2026 at 07:00 UTC", "Export", "Library", keep, "deleted for good"} {
		if !strings.Contains(text, want) {
			t.Fatalf("notice lacks %q:\n%s", want, text)
		}
	}
	if subject != "Your Misty account is read-only" || !strings.Contains(body, `<a href="`+keep) {
		t.Fatalf("notice = %q\n%s", subject, body)
	}
	if subject, text, _ = closingEmail(true, deleteAt, keep); subject != "Your Misty account will be deleted on November 30" || !strings.Contains(text, "in 3 days") {
		t.Fatalf("warning = %q\n%s", subject, text)
	}
	// A server with no page to send people to names who can help, and nothing about billing.
	_, text, body = closingEmail(false, deleteAt, "")
	if !strings.Contains(text, "contact whoever runs your server") || strings.Contains(body, "<a href") {
		t.Fatalf("no keep link:\n%s", text)
	}
	for _, unwanted := range []string{"subscri", "Misty Pro", "pricing"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(unwanted)) {
			t.Fatalf("the open server's email mentions %q:\n%s", unwanted, text)
		}
	}
}

func TestInvitationEmailEscapesWhatPeopleTyped(t *testing.T) {
	link := "misty://open/invite/token"
	subject, text, body := invitationEmail("Sam <b>", "Team & Co", link)
	if subject != "Sam <b> invited you to Team & Co on Misty" || !strings.Contains(text, link) {
		t.Fatalf("invitation = %q\n%s", subject, text)
	}
	if strings.Contains(body, "<b>") || !strings.Contains(body, "Team &amp; Co") || !strings.Contains(body, link) {
		t.Fatalf("html = %s", body)
	}
	if subject, _, _ = invitationEmail("", "", link); subject != "Someone invited you to a Space on Misty" {
		t.Fatalf("no names = %q", subject)
	}
}

func TestARowWithoutWhatItNeedsMakesNoMessage(t *testing.T) {
	at := time.Now()
	for _, row := range []db.OutgoingEmail{
		{Kind: db.EmailSetup, Link: "https://mistysys.com/setup"},
		{Kind: db.EmailSignIn, SetupKey: "misty_setup_abc"},
		{Kind: db.EmailClosed},
		{Kind: db.EmailInvitation},
		{Kind: "unknown", Link: "x", SetupKey: "y", DeleteAt: &at},
	} {
		if _, ok := messageFor(row); ok {
			t.Fatalf("%+v made a message", row)
		}
	}
	message, ok := messageFor(db.OutgoingEmail{ID: "row-1", Kind: db.EmailSetup, Recipient: "a@example.com", SetupKey: "misty_setup_abc",
		Link: "https://mistysys.com/setup#server=api.example.com&key=misty_setup_abc"})
	if !ok || message.To != "a@example.com" || message.CustomID != "misty-row-1" || !strings.Contains(message.Text, "with the server api.example.com") {
		t.Fatalf("message = %+v %v", message, ok)
	}
	if strings.Contains(message.CustomID, "misty_setup") {
		t.Fatal("the key reached Mailjet's custom ID")
	}
}
