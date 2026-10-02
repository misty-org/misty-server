package db

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// HomeAgendaEntry is an agenda entry labeled with its Space.
type HomeAgendaEntry struct {
	SpaceAgendaEntry
	SpaceID   string `json:"space_id"`
	SpaceName string `json:"space_name"`
}

// HomeAgendaMaxSpaces bounds one Home request; members of more Spaces see the
// earliest entries of their most recently joined ones.
const HomeAgendaMaxSpaces = 200

// HomeAgenda merges the earliest open agenda entries across every active Space
// the user may view tasks in, in one transaction. Each Space applies the same
// permission and audience rules as its own agenda; Spaces without permission
// are skipped rather than failing the whole Home.
func (db *Database) HomeAgenda(ctx context.Context, userID string, from, to time.Time, limit int) ([]HomeAgendaEntry, error) {
	if limit < 1 || limit > 50 {
		limit = 6
	}
	out := []HomeAgendaEntry{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name FROM space_members m JOIN spaces s ON s.id=m.space_id
   WHERE m.user_id=$1 AND s.lifecycle_state='active' ORDER BY m.joined_at DESC,s.id LIMIT $2`, userID, HomeAgendaMaxSpaces)
		if err != nil {
			return err
		}
		type space struct{ id, name string }
		var spaces []space
		for rows.Next() {
			var item space
			if err := rows.Scan(&item.id, &item.name); err != nil {
				rows.Close()
				return err
			}
			spaces = append(spaces, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range spaces {
			allowed, err := hasSpacePermissionTx(ctx, tx, userID, item.id, PermissionTasksView)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			snapshot := SpaceAgendaSnapshot{}
			linked, err := loadSpaceAgendaTasksTx(ctx, tx, userID, item.id, from, to, &snapshot)
			if err != nil {
				return err
			}
			if err := loadSpaceAgendaCalendarTx(ctx, tx, item.id, from, to, linked, &snapshot); err != nil {
				return err
			}
			if err := loadSpaceAgendaNativeCalendarTx(ctx, tx, userID, item.id, from, to, &snapshot); err != nil {
				return err
			}
			if err := loadSpaceAgendaRoadmapDatesTx(ctx, tx, userID, item.id, from, to, &snapshot); err != nil {
				return err
			}
			for _, entry := range snapshot.Entries {
				if entry.Status != "completed" {
					out = append(out, HomeAgendaEntry{SpaceAgendaEntry: entry, SpaceID: item.id, SpaceName: item.name})
				}
			}
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, err
}
