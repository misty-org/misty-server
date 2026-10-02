package db

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

type ConsoleBilling struct {
	Licenses           []ConsoleStateCount
	OutboxPending      int
	OutboxRetrying     int
	OutboxOldest       *time.Time
	Reservations       int
	ReservationsOldest *time.Time
}

func (db *Database) ConsoleBilling(ctx context.Context) (ConsoleBilling, error) {
	var b ConsoleBilling
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		var err error
		if b.Licenses, err = queryStateCounts(ctx, tx, `
			SELECT tier, status, count(*) FROM licenses GROUP BY tier, status ORDER BY 3 DESC`); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			SELECT (SELECT count(*) FROM billing_adapter_outbox WHERE delivered_at IS NULL),
			       (SELECT count(*) FROM billing_adapter_outbox WHERE delivered_at IS NULL AND attempts > 0),
			       (SELECT min(created_at) FROM billing_adapter_outbox WHERE delivered_at IS NULL),
			       (SELECT count(*) FROM billing_adapter_reservations),
			       (SELECT min(created_at) FROM billing_adapter_reservations)`,
		).Scan(&b.OutboxPending, &b.OutboxRetrying, &b.OutboxOldest, &b.Reservations, &b.ReservationsOldest)
	})
	return b, err
}

type ConsoleTableSize struct {
	Name  string
	Bytes int64
	Rows  int64
}

type ConsoleMigration struct {
	Version int64
	Name    string
	Applied bool
}

type ConsoleDatabaseStats struct {
	Name        string
	SizeBytes   int64
	Connections int
	Applied     int64
	Migrations  []ConsoleMigration
	Tables      []ConsoleTableSize
}

// ConsoleDatabaseStats reports schema version, size and the largest tables.
// Catalog views are not subject to RLS, so this needs no scope.
func (db *Database) ConsoleDatabaseStats(ctx context.Context) (ConsoleDatabaseStats, error) {
	var s ConsoleDatabaseStats
	if err := db.Conn.QueryRowContext(ctx, `
		SELECT current_database(), pg_database_size(current_database()),
		       (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
		       COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied), 0)`,
	).Scan(&s.Name, &s.SizeBytes, &s.Connections, &s.Applied); err != nil {
		return s, err
	}
	migrations, err := consoleMigrations(s.Applied)
	if err != nil {
		return s, err
	}
	s.Migrations = migrations
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT relname, pg_total_relation_size(relid), n_live_tup
		FROM pg_stat_user_tables ORDER BY 2 DESC LIMIT 12`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var t ConsoleTableSize
		if err := rows.Scan(&t.Name, &t.Bytes, &t.Rows); err != nil {
			return s, err
		}
		s.Tables = append(s.Tables, t)
	}
	return s, rows.Err()
}

// consoleMigrations lists the embedded migration files, newest first.
func consoleMigrations(applied int64) ([]ConsoleMigration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var migrations []ConsoleMigration
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name()
		version, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			continue
		}
		number, err := strconv.ParseInt(version, 10, 64)
		if err != nil {
			continue
		}
		migrations = append(migrations, ConsoleMigration{
			Version: number,
			Name:    strings.ReplaceAll(rest, "_", " "),
			Applied: number <= applied,
		})
	}
	return migrations, nil
}
