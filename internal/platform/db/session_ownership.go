package db

import (
	"context"
	"time"
)

// WithSessionOwnership runs fn only on the process that holds a PostgreSQL
// session advisory lock for key, so replicated API processes do not repeat the
// same maintenance pass. It reports false when another session owns the work.
// The lock needs a session-compatible connection, like the shared LISTEN session.
func (db *Database) WithSessionOwnership(ctx context.Context, key string, fn func(context.Context) error) (bool, error) {
	conn, err := db.Conn.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var owned bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, "misty-ownership:"+key).Scan(&owned); err != nil || !owned {
		return false, err
	}
	defer func() {
		// Unlock even when the pass was canceled; a failed unlock is released
		// when the pool discards the connection.
		unlock, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlock, `SELECT pg_advisory_unlock(hashtext($1))`, "misty-ownership:"+key)
	}()
	return true, fn(ctx)
}
