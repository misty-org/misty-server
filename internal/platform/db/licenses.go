package db

import (
	"context"
	"database/sql"
)

// A license row is the account's identity record (users.license_id). It
// carries no plan; every account can use every feature.
func createLicenseTx(tx *sql.Tx, licenseID string, userID string) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO licenses (id, user_id) VALUES ($1, $2)`, licenseID, userID)
	return err
}
