package db

import (
	"context"
	"database/sql"
)

func createLicenseTx(tx *sql.Tx, licenseID string, userID string) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO licenses (id, user_id) VALUES ($1, $2)`, licenseID, userID)
	return err
}
