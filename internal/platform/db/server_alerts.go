package db

import (
	"context"
	"database/sql"
	"time"
)

// ServerAlert is a problem the alerts service saw on two checks in a row and
// that has not cleared (deploy/setup/watch.go writes them). The server never
// emails alerts, so the console and the server owner's app show these instead.
type ServerAlert struct {
	Probe    string    `json:"probe"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	OpenedAt time.Time `json:"opened_at"`
}

// OpenServerAlerts lists the open alerts, oldest first.
func (db *Database) OpenServerAlerts(ctx context.Context) ([]ServerAlert, error) {
	out := []ServerAlert{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT probe,title,detail,opened_at FROM server_alerts ORDER BY opened_at,probe`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var alert ServerAlert
			if err := rows.Scan(&alert.Probe, &alert.Title, &alert.Detail, &alert.OpenedAt); err != nil {
				return err
			}
			out = append(out, alert)
		}
		return rows.Err()
	})
	return out, err
}
