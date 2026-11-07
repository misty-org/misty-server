package db

import (
	"context"
	"database/sql"
	"time"
)

// AccountExportAgent is one agent the account listed in a Space. The agent
// itself lives on the person's computer; a Space keeps its name and no more.
type AccountExportAgent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SpaceID   string    `json:"space_id"`
	SpaceName string    `json:"space_name"`
	AddedAt   time.Time `json:"added_at"`
}

func appendAccountAgentExport(ctx context.Context, tx *sql.Tx, userID string, out *AccountPortableExport) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.agent_id,m.agent_name,m.space_id,s.name,m.added_at
		FROM space_agents m JOIN spaces s ON s.id=m.space_id WHERE m.owner_user_id=$1 ORDER BY m.added_at`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item AccountExportAgent
		if err := rows.Scan(&item.ID, &item.Name, &item.SpaceID, &item.SpaceName, &item.AddedAt); err != nil {
			return err
		}
		out.Agents = append(out.Agents, item)
	}
	return rows.Err()
}
