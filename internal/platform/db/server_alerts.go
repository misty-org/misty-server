package db

import (
	"context"
	"database/sql"
	"time"
)

type ServerAlert struct {
	Probe    string    `json:"probe"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	OpenedAt time.Time `json:"opened_at"`
}

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
