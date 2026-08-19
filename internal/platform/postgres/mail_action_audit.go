package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// MailActionAudit is intentionally content-free. It records only the fact and
// outcome of a mailbox mutation so product safety can be reviewed without
// persisting email content, recipients, subjects, or attachment metadata.
type MailActionAudit struct {
	ID           int64
	UserID       string
	ConnectionID string
	Action       string
	TargetType   string
	TargetID     string
	Source       string
	Confirmed    bool
	Success      bool
	ErrorCode    string
	CreatedAt    time.Time
}

func (db *Database) RecordMailActionAudit(ctx context.Context, item MailActionAudit) error {
	item.UserID = strings.TrimSpace(item.UserID)
	item.ConnectionID = strings.TrimSpace(item.ConnectionID)
	item.TargetID = strings.TrimSpace(item.TargetID)
	item.ErrorCode = strings.TrimSpace(item.ErrorCode)
	if item.UserID == "" || item.ConnectionID == "" || item.TargetID == "" || len(item.TargetID) > 320 || len(item.ErrorCode) > 120 ||
		!oneOf(item.Action, "thread_modify", "draft_create", "draft_update", "draft_send") ||
		!oneOf(item.TargetType, "thread", "draft") || !oneOf(item.Source, "user", "ai") {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(item.UserID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO mail_action_audit
			(user_id,connection_id,action,target_type,target_id,source,confirmed,success,error_code)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id,created_at`, item.UserID, item.ConnectionID, item.Action,
			item.TargetType, item.TargetID, item.Source, item.Confirmed, item.Success,
			item.ErrorCode).Scan(&item.ID, &item.CreatedAt)
	})
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
