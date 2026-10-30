package db

import (
	"context"
	"database/sql"
	"log"
)

// A license row is the account's identity record (users.license_id). It
// carries no plan; every account can use every feature.
func createLicenseTx(tx *sql.Tx, licenseID string, userID string) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO licenses (id, user_id, license_device) VALUES ($1, $2, '')`, licenseID, userID)
	return err
}

func (db *Database) UpdateLicenseDevice(userID, device string) error {
	err := db.WithRLSContext(context.Background(), userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
			UPDATE licenses
			SET license_device = $2,
				updated_at = NOW()
			WHERE user_id = $1
		`, userID, device)
		return err
	})
	if err != nil {
		log.Println("Failed to update license device:", err)
	}
	return err
}
