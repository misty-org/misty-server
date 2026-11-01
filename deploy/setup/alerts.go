package main

import (
	"context"
	"database/sql"
	"fmt"
)

// pgAlertBook writes open alerts to the server_alerts table as the API's own
// restricted role, in service mode (the table's row security admits only
// that), the way the API reads them.
type pgAlertBook struct{ database *sql.DB }

func openAlertBook(store *Store) (*pgAlertBook, error) {
	if !store.Has("DB_PASSWORD") {
		return nil, fmt.Errorf("the database password is missing; the setup service must run first")
	}
	database, err := sql.Open("postgres", fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		envOr("MISTY_DB_HOST", dbHost), dbAppRole, store.Get("DB_PASSWORD"), dbName))
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	return &pgAlertBook{database: database}, nil
}

func (b *pgAlertBook) close() { _ = b.database.Close() }

func (b *pgAlertBook) inService(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := b.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *pgAlertBook) openAlerts(ctx context.Context) (map[string]bool, error) {
	open := map[string]bool{}
	err := b.inService(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT probe FROM server_alerts`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var probe string
			if err := rows.Scan(&probe); err != nil {
				return err
			}
			open[probe] = true
		}
		return rows.Err()
	})
	return open, err
}

func (b *pgAlertBook) raise(ctx context.Context, probe, title, detail string) error {
	return b.inService(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO server_alerts(probe,title,detail) VALUES($1,$2,$3)
			ON CONFLICT(probe) DO UPDATE SET title=EXCLUDED.title,detail=EXCLUDED.detail,updated_at=now()`, probe, title, detail)
		return err
	})
}

func (b *pgAlertBook) clear(ctx context.Context, probe string) error {
	return b.inService(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM server_alerts WHERE probe=$1`, probe)
		return err
	})
}
