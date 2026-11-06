package email

import (
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// messageFor turns an outbox row into its email, or reports that it can't.
func messageFor(row db.OutgoingEmail) (Message, bool) {
	m := Message{To: row.Recipient, CustomID: "misty-" + row.ID}
	switch row.Kind {
	case db.EmailSetup, db.EmailSignIn:
		if row.Link == "" || row.SetupKey == "" {
			return m, false
		}
		m.Subject, m.Text, m.HTML = keyEmail(row.Kind == db.EmailSignIn, row.Link, setupServer(row.Link), row.SetupKey)
	case db.EmailClosed, db.EmailDeletionWarning:
		if row.DeleteAt == nil {
			return m, false
		}
		m.Subject, m.Text, m.HTML = closingEmail(row.Kind == db.EmailDeletionWarning, *row.DeleteAt, row.Link)
	case db.EmailInvitation:
		if row.Link == "" {
			return m, false
		}
		m.Subject, m.Text, m.HTML = invitationEmail(row.InviterName, row.SpaceName, row.Link)
	case db.EmailConfirm:
		if row.Link == "" {
			return m, false
		}
		m.Subject, m.Text, m.HTML = confirmEmail(row.Link)
	default:
		return m, false
	}
	return m, true
}

// setupServer reads the server's address from a setup link's fragment.
func setupServer(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	fragment, _ := url.ParseQuery(parsed.Fragment)
	return fragment.Get("server")
}

// keyEmail is the setup email for a new account, or the sign-in email for one
// that has signed in before: the link, and the server and key to type instead.
func keyEmail(signIn bool, link, server, key string) (subject, text, htmlBody string) {
	subject, opening, button := "Your Misty account is ready", "Your Misty account is ready.", "Set up Misty"
	closing := "The key works once and expires in 7 days. After it signs you in, Misty asks you to choose a password."
	if signIn {
		subject, opening, button = "Sign in to Misty", "Here is your sign-in link.", "Sign in to Misty"
		closing = "The key works once and expires in 7 days. It also lets you set a new password. If you didn't ask for it, ignore this email; your password stays the same."
	}
	open := "Open this link on the computer where Misty is installed:"
	typed := "Or, in Misty's setup, enter this key"
	if server != "" {
		typed += " with the server " + server
	}
	typed += ":"
	text = strings.Join([]string{opening, open, link, typed, key, closing}, "\n\n") + "\n"
	htmlBody = htmlStart + paragraph(opening) + paragraph(open) +
		`<p style="margin:0 0 16px"><a href="` + html.EscapeString(link) + `" style="display:inline-block;padding:10px 16px;background:#111;color:#fff;text-decoration:none;border-radius:6px">` + button + `</a></p>` +
		`<p style="margin:0 0 16px;word-break:break-all;color:#555">` + html.EscapeString(link) + `</p>` +
		paragraph(typed) + monospace(key) + paragraph(closing) + htmlEnd
	return subject, text, htmlBody
}

// closingEmail is the notice when a closed account becomes read-only, or the
// warning 3 days before it is deleted. keepURL, when the operator named one, is
// where the person can keep the account.
func closingEmail(warning bool, deleteAt time.Time, keepURL string) (subject, text, htmlBody string) {
	date := deleteAt.UTC().Format("January 2, 2006 at 15:04 UTC")
	subject = "Your Misty account is read-only"
	opening := "Your Misty account has been closed, so it is now read-only."
	if warning {
		subject = "Your Misty account will be deleted on " + deleteAt.UTC().Format("January 2")
		opening = "Your Misty account is still read-only, and it will be deleted in 3 days."
	}
	keep := "To keep your account as it is, contact whoever runs your server before then."
	if keepURL != "" {
		keep = "To keep your account as it is, go here before then: " + keepURL
	}
	paragraphs := []string{
		opening,
		"You can still sign in and export everything until " + date + ". To export, choose Export on the notice in the Misty app; your files are also in the Library.",
		keep,
		"After " + date + ", your account and everything in it are deleted for good.",
	}
	text = strings.Join(paragraphs, "\n\n") + "\n"
	var body strings.Builder
	body.WriteString(htmlStart)
	for _, p := range paragraphs {
		if keepURL != "" && p == keep {
			body.WriteString(`<p style="margin:0 0 16px">` + html.EscapeString(strings.TrimSuffix(p, keepURL)) + `<a href="` + html.EscapeString(keepURL) + `" style="color:#111">` + html.EscapeString(keepURL) + `</a></p>`)
			continue
		}
		body.WriteString(paragraph(p))
	}
	body.WriteString(htmlEnd)
	return subject, text, body.String()
}

// invitationEmail carries a Space invitation's link. The link opens Misty, and
// many mail apps won't follow a misty:// address, so it is shown to copy.
func invitationEmail(inviter, space, link string) (subject, text, htmlBody string) {
	if inviter == "" {
		inviter = "Someone"
	}
	if space == "" {
		space = "a Space"
	}
	subject = inviter + " invited you to " + space + " on Misty"
	opening := inviter + " invited you to " + space + " on Misty."
	open := "With Misty installed, paste this link into Misty's address bar to accept:"
	closing := "If you weren't expecting this, you can ignore it."
	text = strings.Join([]string{opening, open, link, closing}, "\n\n") + "\n"
	htmlBody = htmlStart + paragraph(opening) + paragraph(open) + monospace(link) + paragraph(closing) + htmlEnd
	return subject, text, htmlBody
}

// confirmEmail carries the link that finishes a new account.
func confirmEmail(link string) (subject, text, htmlBody string) {
	subject = "Confirm your email for Misty"
	opening := "Confirm your email to finish creating your Misty account."
	closing := "The link expires in 24 hours. If you didn't create this account, ignore this email and don't open the link."
	text = strings.Join([]string{opening, link, closing}, "\n\n") + "\n"
	htmlBody = htmlStart + paragraph(opening) +
		`<p style="margin:0 0 16px"><a href="` + html.EscapeString(link) + `" style="display:inline-block;padding:10px 16px;background:#111;color:#fff;text-decoration:none;border-radius:6px">Confirm email</a></p>` +
		`<p style="margin:0 0 16px;word-break:break-all;color:#555">` + html.EscapeString(link) + `</p>` +
		paragraph(closing) + htmlEnd
	return subject, text, htmlBody
}

const (
	htmlStart =`<!doctype html><html><body style="margin:0;padding:24px;background:#fff;color:#111;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;font-size:15px;line-height:1.5"><div style="max-width:520px">`
	htmlEnd   = `<p style="margin:24px 0 0;color:#777;font-size:13px">Misty</p></div></body></html>`
)

func paragraph(text string) string {
	return `<p style="margin:0 0 16px">` + html.EscapeString(text) + `</p>`
}

func monospace(text string) string {
	return `<p style="margin:0 0 16px;font-family:ui-monospace,Menlo,monospace;font-size:15px;word-break:break-all">` + html.EscapeString(text) + `</p>`
}
