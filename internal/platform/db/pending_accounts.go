package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/misty-org/misty-server/internal/accounts"
)

const (
	// PendingAccountTTL is how long a confirmation link works.
	PendingAccountTTL = 24 * time.Hour
	// pendingAccountResend spaces out the emails one address can be sent.
	pendingAccountResend = 2 * time.Minute
)

// ErrPendingAccountInvalid means a confirmation link is wrong, used or expired.
var ErrPendingAccountInvalid = errors.New("this confirmation link is no longer valid")

// PendingAccount is what a sign-up keeps until its email is confirmed.
type PendingAccount struct {
	Username, Email, Password string
}

// QueuePendingAccount stores a sign-up and its confirmation email. The newest one for
// an email wins, so a stranger's sign-up never locks out the address's owner, and its
// link is the only one that works. Within pendingAccountResend of the last email it
// does nothing, so nobody can flood an address.
func (db *Database) QueuePendingAccount(ctx context.Context, account PendingAccount, tokenHash, link string) error {
	username, err := accounts.NormalizeUsername(account.Username)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(account.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	address := accounts.NormalizeEmail(account.Email)
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, username).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrUsernameTaken
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO pending_accounts(email,username,password_hash,token_hash,expires_at)
			VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (email) DO UPDATE SET username=EXCLUDED.username,password_hash=EXCLUDED.password_hash,
				token_hash=EXCLUDED.token_hash,expires_at=EXCLUDED.expires_at,sent_at=now()
			WHERE pending_accounts.sent_at<now()-$6*interval '1 second'`,
			address, username, string(hash), tokenHash, time.Now().Add(PendingAccountTTL), pendingAccountResend.Seconds())
		if err != nil {
			return err
		}
		return queueConfirmEmailTx(ctx, tx, result, address, link)
	})
}

// ResendPendingAccount sends a live sign-up a new link, unless it was sent one
// within pendingAccountResend.
func (db *Database) ResendPendingAccount(ctx context.Context, email, tokenHash, link string) error {
	address := accounts.NormalizeEmail(email)
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE pending_accounts SET token_hash=$2,expires_at=$3,sent_at=now()
			WHERE email=$1 AND expires_at>now() AND sent_at<now()-$4*interval '1 second'`,
			address, tokenHash, time.Now().Add(PendingAccountTTL), pendingAccountResend.Seconds())
		if err != nil {
			return err
		}
		return queueConfirmEmailTx(ctx, tx, result, address, link)
	})
}

// queueConfirmEmailTx stores the email for a link just issued, when one was.
// Earlier links stopped working, so their unsent emails are cancelled with them.
func queueConfirmEmailTx(ctx context.Context, tx *sql.Tx, issued sql.Result, address, link string) error {
	if count, err := issued.RowsAffected(); err != nil || count == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason='superseded',link=NULL,updated_at=now()
		WHERE kind='confirm_email' AND recipient=$1 AND state='pending'`, address); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO email_outbox(kind,recipient,link) VALUES('confirm_email',$1,$2)`, address, link)
	return err
}

// PendingAccountPassword is the password hash of a live sign-up, or "" without one.
func (db *Database) PendingAccountPassword(ctx context.Context, email string) (string, error) {
	var hash string
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT password_hash FROM pending_accounts WHERE email=$1 AND expires_at>now()`,
			accounts.NormalizeEmail(email)).Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// ConfirmPendingAccount makes the account a confirmation link stands for. Its email
// may have gained an account meanwhile, through Google or the operator: that account
// stays as it is and the answer is ErrEmailTaken.
func (db *Database) ConfirmPendingAccount(ctx context.Context, tokenHash string) (*User, error) {
	var address, username, hash string
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT email,username,password_hash FROM pending_accounts
			WHERE token_hash=$1 AND expires_at>now()`, strings.TrimSpace(tokenHash)).Scan(&address, &username, &hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPendingAccountInvalid
	}
	if err != nil {
		return nil, err
	}
	user, err := db.accountStore().CreateConfirmedUser(username, address, hash)
	if err != nil && !errors.Is(err, ErrEmailTaken) {
		return nil, err
	}
	if forgetErr := db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM pending_accounts WHERE email=$1`, address)
		return err
	}); forgetErr != nil {
		return nil, forgetErr
	}
	return user, err
}
