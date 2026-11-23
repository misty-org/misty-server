package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const (
	EmailSetup           = "setup"
	EmailSignIn          = "sign_in"
	EmailClosed          = "closed"
	EmailDeletionWarning = "deletion_warning"
	EmailInvitation      = "invitation"
	EmailConfirm         = "confirm_email"

	emailWarnBefore = 3 * 24 * time.Hour
	emailLease      = 5 * time.Minute
)

type KeyEmail struct {
	Kind, Recipient, Key, Link string
}

type OutgoingEmail struct {
	ID, Kind, Recipient, SetupKey, Link, SpaceName, InviterName string
	DeleteAt                                                    *time.Time
}

func queueKeyEmailTx(ctx context.Context, tx *sql.Tx, userID string, email KeyEmail) error {
	if _, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason='superseded',setup_key=NULL,link=NULL,updated_at=now()
		WHERE user_id=$1 AND kind IN ('setup','sign_in') AND state='pending'`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO email_outbox(kind,user_id,recipient,setup_key,link) VALUES($1,$2,$3,$4,$5)`,
		email.Kind, userID, strings.TrimSpace(email.Recipient), email.Key, email.Link)
	return err
}

func (db *Database) QueueClosingEmails(ctx context.Context, userID, recipient, keepURL string, deleteAt time.Time) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO email_outbox(kind,user_id,recipient,link,delete_at,send_after)
			VALUES('closed',$1,$2,NULLIF($3,''),$4,now()),('deletion_warning',$1,$2,NULLIF($3,''),$4,$4::timestamptz-$5*interval '1 second')
			ON CONFLICT (user_id,kind,delete_at) WHERE kind IN ('closed','deletion_warning') DO NOTHING`,
			userID, strings.TrimSpace(recipient), keepURL, deleteAt.UTC(), emailWarnBefore.Seconds())
		return err
	})
}

func (db *Database) CancelClosingEmails(ctx context.Context, userID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason='resumed',updated_at=now()
			WHERE user_id=$1 AND kind IN ('closed','deletion_warning') AND state='pending'`, userID)
		return err
	})
}

func (db *Database) QueueInvitationEmail(ctx context.Context, invitationID, recipient, link, spaceName, inviterName string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := cancelInvitationEmailTx(ctx, tx, invitationID, "superseded"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO email_outbox(kind,invitation_id,recipient,link,space_name,inviter_name) VALUES('invitation',$1,$2,$3,$4,$5)`,
			invitationID, strings.TrimSpace(recipient), link, spaceName, inviterName)
		return err
	})
}

func (db *Database) CancelInvitationEmail(ctx context.Context, invitationID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		return cancelInvitationEmailTx(ctx, tx, invitationID, "revoked")
	})
}

func cancelInvitationEmailTx(ctx context.Context, tx *sql.Tx, invitationID, reason string) error {
	_, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason=$2,link=NULL,updated_at=now()
		WHERE invitation_id=$1 AND state='pending'`, invitationID, reason)
	return err
}

func (db *Database) ClaimDueEmails(ctx context.Context, limit int) ([]OutgoingEmail, error) {
	due := []OutgoingEmail{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM email_outbox WHERE state<>'pending' AND updated_at<now()-interval '30 days'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_accounts WHERE expires_at<=now()`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason='expired',setup_key=NULL,link=NULL,updated_at=now()
			WHERE state='pending' AND ((kind IN ('setup','sign_in','invitation') AND created_at<now()-interval '7 days')
				OR (kind='confirm_email' AND created_at<now()-interval '1 day') OR delete_at<=now())`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE email_outbox w SET state='cancelled',cancel_reason='notice_late',updated_at=now()
			FROM email_outbox n WHERE w.kind='deletion_warning' AND w.state='pending' AND n.kind='closed'
			AND n.user_id=w.user_id AND n.delete_at=w.delete_at
			AND (n.state='cancelled' OR (n.state='sent' AND n.sent_at>=w.delete_at-$1*interval '1 second'))`, emailWarnBefore.Seconds()); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `UPDATE email_outbox SET send_after=now()+$1*interval '1 second',updated_at=now()
			WHERE id IN (SELECT e.id FROM email_outbox e WHERE e.state='pending' AND e.send_after<=now()
				AND (e.kind<>'deletion_warning' OR EXISTS (SELECT 1 FROM email_outbox n WHERE n.kind='closed' AND n.state='sent'
					AND n.user_id=e.user_id AND n.delete_at=e.delete_at))
				ORDER BY e.send_after LIMIT $2 FOR UPDATE SKIP LOCKED)
			RETURNING id::text,kind,recipient,COALESCE(setup_key,''),COALESCE(link,''),COALESCE(space_name,''),COALESCE(inviter_name,''),delete_at`,
			emailLease.Seconds(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var email OutgoingEmail
			var deleteAt sql.NullTime
			if err := rows.Scan(&email.ID, &email.Kind, &email.Recipient, &email.SetupKey, &email.Link, &email.SpaceName, &email.InviterName, &deleteAt); err != nil {
				return err
			}
			if deleteAt.Valid {
				at := deleteAt.Time
				email.DeleteAt = &at
			}
			due = append(due, email)
		}
		return rows.Err()
	})
	return due, err
}

func (db *Database) MarkEmailSent(ctx context.Context, id string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='sent',sent_at=now(),attempts=attempts+1,last_error=NULL,setup_key=NULL,
			link=CASE WHEN kind IN ('closed','deletion_warning') THEN link END,updated_at=now() WHERE id=$1`, id)
		return err
	})
}

func (db *Database) MarkEmailFailed(ctx context.Context, id, code string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_outbox SET attempts=attempts+1,last_error=$2,updated_at=now(),
			send_after=now()+least(interval '1 minute'*power(2,least(attempts,6)),interval '1 hour') WHERE id=$1 AND state='pending'`, id, code)
		return err
	})
}

func (db *Database) CancelUnusableEmail(ctx context.Context, id string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_outbox SET state='cancelled',cancel_reason='unusable',setup_key=NULL,link=NULL,updated_at=now() WHERE id=$1`, id)
		return err
	})
}
