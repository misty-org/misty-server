package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"

	"github.com/kannachi323/misty/server/internal/accounts"
)

const (
	rlsModeSetting = "app.rls_mode"
)

const (
	rlsModeService = "service"
)

func (db *Database) TestingWithRLSContext(ctx context.Context, settings map[string]string, fn func(*sql.Tx) error) error {
	if db.Conn == nil {
		return errors.New("database connection is not initialized")
	}

	tx, err := db.Conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			rollbackTx(tx)
		}
	}()

	for key, value := range settings {
		if _, err := tx.ExecContext(ctx, `SELECT set_config($1, $2, true)`, key, value); err != nil {
			return err
		}
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := cloudusage.Commit(ctx, tx); err != nil {
		return err
	}
	committed = true
	return nil
}

func TestingServiceRLSSettings() map[string]string {
	return map[string]string{
		rlsModeSetting: rlsModeService,
	}
}

func sessionCreateRLSSettings(hash, user string) map[string]string {
	return accounts.SessionCreateScope(hash, user)
}
func userRLSSettings(user string) map[string]string { return accounts.UserScope(user) }
