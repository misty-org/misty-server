package db

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
)

// ManualSummaryPrefix starts the surface id of a summary the person wrote by hand, so it can be
// told from a recap Misty wrote on a schedule.
const ManualSummaryPrefix = "manual-"

// AddManualSummary keeps a summary the person wrote in one Space's tree. It is a recap that never
// runs: it is off, so no schedule picks it up, and its result is the text. It returns the id the
// tree uses for it ("summary:<surface>").
func (db *Database) AddManualSummary(ctx context.Context, user, spaceID, title, text string) (string, error) {
	title, text = strings.TrimSpace(title), strings.TrimSpace(text)
	if user == "" || text == "" || len(text) > 20000 || len(title) > 200 {
		return "", ErrSpaceInvalid
	}
	surface := ManualSummaryPrefix + uuid.NewString()
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		space, err := memorySpaceTx(ctx, tx, user, spaceID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_recaps(user_id,space_id,surface_id,enabled,cadence,prompt,last_result,last_run_at)
 VALUES($1,$2,$3,false,'daily',$4,$5,now())`, user, space, surface, title, text)
		return err
	})
	if err != nil {
		return "", err
	}
	return "summary:" + surface, nil
}
