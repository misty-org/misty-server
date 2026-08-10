package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	rlsModeWaitlist     = "waitlist"
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

func anonymousRLSSettings(email string) map[string]string {
	return map[string]string{
		rlsModeSetting:         rlsModeAnonymous,
		rlsCurrentEmailSetting: normalizeEmail(email),
	}
}

func registrationRLSSettings(userID, licenseID, email string) map[string]string {
	return map[string]string{
		rlsModeSetting:         rlsModeRegistration,
		rlsCurrentUserSetting:  strings.TrimSpace(userID),
		rlsLicenseIDSetting:    strings.TrimSpace(licenseID),
		rlsCurrentEmailSetting: normalizeEmail(email),
	}
}

func TestingServiceRLSSettings() map[string]string {
	return map[string]string{
		rlsModeSetting: rlsModeService,
	}
}

func sessionRLSSettings(tokenHash string) map[string]string {
	return map[string]string{
		rlsModeSetting:        rlsModeSession,
		rlsSessionHashSetting: strings.TrimSpace(tokenHash),
	}
}

func sessionCreateRLSSettings(tokenHash, userID string) map[string]string {
	settings := sessionRLSSettings(tokenHash)
	settings[rlsCurrentUserSetting] = strings.TrimSpace(userID)
	return settings
}

func userRLSSettings(userID string) map[string]string {
	return map[string]string{
		rlsModeSetting:        rlsModeUser,
		rlsCurrentUserSetting: strings.TrimSpace(userID),
	}
}

func waitlistRLSSettings(email string) map[string]string {
	return map[string]string{
		rlsModeSetting:         rlsModeWaitlist,
		rlsCurrentEmailSetting: normalizeEmail(email),
	}
}
