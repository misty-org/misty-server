package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Operator console queries. They run in service RLS scope because the console
// administers every account; the console process is loopback-only and never
// mounted on the public API.

type ConsoleAccount struct {
	ID        string
	Email     string
	Username  string
	Name      string
	State     string
	Tier      string
	CreatedAt time.Time
	Devices   int
	Sessions  int
}

type ConsoleDevice struct {
	Name     string
	Platform string
	LastSeen time.Time
	Revoked  bool
}

type ConsoleAccountDetail struct {
	ConsoleAccount
	LicenseStatus  string
	StorageBytes   int64
	SyncWorkspaces int // per-device-sign-in workspaces, excluding the shared one
	DeviceList     []ConsoleDevice
}

func (db *Database) consoleTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), fn)
}

const consoleAccountColumns = `
	u.id, u.email, u.username, u.name, u.lifecycle_state, u.created_at,
	COALESCE(l.tier, ''),
	(SELECT count(*) FROM trusted_devices d WHERE d.user_id = u.id AND d.revoked_at IS NULL),
	(SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > now())
	FROM users u
	LEFT JOIN licenses l ON l.id = u.license_id`

func scanConsoleAccount(row interface{ Scan(...any) error }, a *ConsoleAccount) error {
	return row.Scan(&a.ID, &a.Email, &a.Username, &a.Name, &a.State, &a.CreatedAt,
		&a.Tier, &a.Devices, &a.Sessions)
}

// ConsoleListAccounts returns the newest accounts matching an email, username
// or name fragment.
func (db *Database) ConsoleListAccounts(ctx context.Context, search string, limit int) ([]ConsoleAccount, error) {
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(search)) + "%"
	var accounts []ConsoleAccount
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+consoleAccountColumns+`
			WHERE u.lifecycle_state <> 'deleted'
			  AND (u.email ILIKE $1 OR u.username ILIKE $1 OR u.name ILIKE $1)
			ORDER BY u.created_at DESC LIMIT $2`, pattern, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a ConsoleAccount
			if err := scanConsoleAccount(rows, &a); err != nil {
				return err
			}
			accounts = append(accounts, a)
		}
		return rows.Err()
	})
	return accounts, err
}

func (db *Database) ConsoleAccountDetail(ctx context.Context, userID string) (ConsoleAccountDetail, error) {
	var d ConsoleAccountDetail
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+consoleAccountColumns+` WHERE u.id = $1`, userID)
		if err := scanConsoleAccount(row, &d.ConsoleAccount); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE((SELECT l.status FROM licenses l JOIN users u ON u.license_id = l.id WHERE u.id = $1), ''),
			       COALESCE((SELECT used_bytes FROM owner_storage_usage WHERE owner_user_id = $1), 0),
			       (SELECT count(*) FROM browser_sync_workspaces w
			        JOIN browser_sync_vaults v ON v.vault_id = w.vault_id
			        WHERE v.user_id = $1 AND w.workspace_id <> w.vault_id)`, userID,
		).Scan(&d.LicenseStatus, &d.StorageBytes, &d.SyncWorkspaces); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT name, platform, last_seen_at, revoked_at IS NOT NULL
			FROM trusted_devices WHERE user_id = $1
			ORDER BY revoked_at IS NOT NULL, last_seen_at DESC LIMIT 20`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var device ConsoleDevice
			if err := rows.Scan(&device.Name, &device.Platform, &device.LastSeen, &device.Revoked); err != nil {
				return err
			}
			d.DeviceList = append(d.DeviceList, device)
		}
		return rows.Err()
	})
	return d, err
}

// ConsoleRevokeSessions signs the account out everywhere.
func (db *Database) ConsoleRevokeSessions(ctx context.Context, userID string) (int64, error) {
	var revoked int64
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		revoked, err = result.RowsAffected()
		return err
	})
	return revoked, err
}
