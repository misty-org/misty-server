package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kannachi323/misty/server/internal/accounts"
)

const (
	rlsModeSetting         = "app.rls_mode"
	rlsCurrentUserSetting  = "app.current_user_id"
	rlsCurrentEmailSetting = "app.current_email"
	rlsLicenseIDSetting    = "app.current_license_id"
	rlsSessionHashSetting  = "app.current_session_token_hash"
)

const (
	rlsModeAnonymous    = "anonymous"
	rlsModeRegistration = "registration"
	rlsModeService      = "service"
	rlsModeSession      = "session"
	rlsModeUser         = "user"
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

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func anonymousRLSSettings(email string) map[string]string { return accounts.AnonymousScope(email) }
func registrationRLSSettings(user, id, email string) map[string]string {
	return accounts.RegistrationScope(user, id, email)
}

func TestingServiceRLSSettings() map[string]string {
	return map[string]string{
		rlsModeSetting: rlsModeService,
	}
}

func sessionRLSSettings(hash string) map[string]string { return accounts.SessionScope(hash) }
func sessionCreateRLSSettings(hash, user string) map[string]string {
	return accounts.SessionCreateScope(hash, user)
}
func userRLSSettings(user string) map[string]string { return accounts.UserScope(user) }
