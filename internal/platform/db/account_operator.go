package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
)

// OperatorAccount is one account as the operator account API and the console
// show it: who it is, its limits and what it uses against them.
type OperatorAccount struct {
	ID           string        `json:"id"`
	Email        string        `json:"email"`
	Username     string        `json:"username"`
	CreatedAt    time.Time     `json:"created_at"`
	Owner        bool          `json:"owner"`
	Suspended    bool          `json:"suspended"`
	SuspendedAt  *time.Time    `json:"suspended_at,omitempty"`
	DeleteAt     *time.Time    `json:"delete_at"`
	PeriodAnchor *time.Time    `json:"period_anchor"`
	Limits       AccountLimits `json:"limits"`
	Usage        AccountUse    `json:"usage"`
	Setup        SetupState    `json:"setup"`
	StorageKnown bool          `json:"-"`
}

// AccountUse is what the account uses against each limit. PeriodStart is the
// start of its current billing period.
type AccountUse struct {
	StorageBytes *int64    `json:"storage_bytes,omitempty"`
	PeriodStart  time.Time `json:"period_start"`
}

// CreateOperatorAccount makes a password-less account for the email with the
// given limits, or, when the email already has an account, applies the limits
// to it. created reports which. It is safe to call again with the same email.
func (db *Database) CreateOperatorAccount(ctx context.Context, email string, limits AccountLimits) (user *User, created bool, err error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") || len(email) > 320 {
		return nil, false, ErrSpaceInvalid
	}
	for attempt := 0; attempt < 6 && user == nil; attempt++ {
		var existing *User
		if existing, _, err = db.GetUserByEmail(email); err != nil {
			return nil, false, err
		}
		if existing != nil {
			user = existing
			break
		}
		// An account being deleted still holds its email until the purge.
		var pending bool
		if err = db.serviceTx(ctx, func(tx *sql.Tx) error {
			pending, err = accountPendingDeletionTx(ctx, tx, email)
			return err
		}); err != nil {
			return nil, false, err
		}
		if pending {
			return nil, false, ErrAccountPendingDeletion
		}
		username := accounts.DefaultUsernameForEmail(email)
		if attempt > 0 {
			suffix := "_" + strconv.Itoa(1000+int(time.Now().UnixNano()%9000))
			username = strings.TrimRight(username[:min(len(username), 30-len(suffix))], "_") + suffix
		}
		user, err = db.accountStore().CreatePasswordlessUser(username, email)
		switch {
		case err == nil:
			created = true
		case errors.Is(err, ErrUsernameTaken), errors.Is(err, ErrEmailTaken):
			// Another create won the email, or the name is taken: look again.
			user, err = nil, nil
		default:
			return nil, false, err
		}
	}
	if user == nil {
		return nil, false, errors.New("could not choose a username for the account")
	}
	if err = db.SetAccountLimits(ctx, user.ID, limits); err != nil {
		return nil, false, err
	}
	return user, created, nil
}

const operatorAccountColumns = `u.id,u.email,u.username,u.created_at,
	u.id=(SELECT id FROM users ORDER BY created_at,id LIMIT 1),
	c.suspended_at,c.delete_at,c.period_anchor,c.storage_bytes,c.devices,c.shared_spaces,c.space_members
	FROM users u LEFT JOIN account_controls c ON c.user_id=u.id`

func scanOperatorAccount(row interface{ Scan(...any) error }) (OperatorAccount, error) {
	var a OperatorAccount
	var suspended, deleteAt, anchor sql.NullTime
	var storage, devices, sharedSpaces, spaceMembers sql.NullInt64
	if err := row.Scan(&a.ID, &a.Email, &a.Username, &a.CreatedAt, &a.Owner, &suspended, &deleteAt, &anchor,
		&storage, &devices, &sharedSpaces, &spaceMembers); err != nil {
		return a, err
	}
	if deleteAt.Valid {
		a.DeleteAt = &deleteAt.Time
	}
	if anchor.Valid {
		a.PeriodAnchor = &anchor.Time
	}
	a.Limits = AccountLimits{
		StorageBytes: nullableInt(storage), Devices: nullableInt(devices),
		SharedSpaces: nullableInt(sharedSpaces), SpaceMembers: nullableInt(spaceMembers),
	}
	if suspended.Valid {
		a.Suspended, a.SuspendedAt = true, &suspended.Time
	}
	return a, nil
}

// fillOperatorAccountTx adds the billing period and the setup state; storage is
// measured only when asked, since it sums every stored payload.
func fillOperatorAccountTx(ctx context.Context, tx *sql.Tx, a *OperatorAccount, storage bool) error {
	err := tx.QueryRowContext(ctx, `SELECT misty_usage_period((SELECT period_anchor FROM account_controls WHERE user_id=$1), now())`, a.ID).Scan(&a.Usage.PeriodStart)
	if err != nil {
		return err
	}
	if a.Setup, err = accountSetupStateTx(ctx, tx, a.ID); err != nil {
		return err
	}
	if storage {
		used, err := accountStorageUsedTx(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		a.Usage.StorageBytes, a.StorageKnown = &used, true
	}
	return nil
}

// OperatorAccountByID returns one active account with its storage measured.
func (db *Database) OperatorAccountByID(ctx context.Context, userID string) (OperatorAccount, error) {
	var a OperatorAccount
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		a, err = scanOperatorAccount(tx.QueryRowContext(ctx, `SELECT `+operatorAccountColumns+` WHERE u.id=$1 AND u.lifecycle_state='active'`, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountNotFound
		}
		if err != nil {
			return err
		}
		return fillOperatorAccountTx(ctx, tx, &a, true)
	})
	return a, err
}

// OperatorAccountSearch picks the accounts OperatorAccounts lists: Email, when
// set, is one exact email (compared as stored, lower case); otherwise Text is
// a fragment of an email or username, and empty matches every account.
type OperatorAccountSearch struct {
	Text  string
	Email string
}

// OperatorAccounts lists the active accounts the search matches, newest
// first. Storage is not measured in the list.
func (db *Database) OperatorAccounts(ctx context.Context, search OperatorAccountSearch, limit int) ([]OperatorAccount, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	where, arg := `(u.email ILIKE $1 OR u.username ILIKE $1)`, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(search.Text))+"%"
	if strings.TrimSpace(search.Email) != "" {
		where, arg = `lower(u.email)=$1`, normalizeEmail(search.Email)
	}
	out := []OperatorAccount{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+operatorAccountColumns+`
			WHERE u.lifecycle_state='active' AND `+where+`
			ORDER BY u.created_at DESC LIMIT $2`, arg, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			a, err := scanOperatorAccount(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out {
			if err := fillOperatorAccountTx(ctx, tx, &out[i], false); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
