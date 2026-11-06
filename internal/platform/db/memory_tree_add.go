package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ManualSummaryPrefix starts the surface id of a summary the person wrote by hand, so it can be
// told from a recap Misty wrote on a schedule.
const ManualSummaryPrefix = "manual-"

// RecapSummaryPrefix starts the surface id of one day's recap. A day has one, and writing it
// again replaces it.
const RecapSummaryPrefix = "recap-"

// RecapThread is one thing a person did that day, with when it started and stopped ("15:04").
type RecapThread struct {
	Title string `json:"title"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// RecapOutline is what a day's recap names beside its text.
type RecapOutline struct {
	Threads []RecapThread `json:"threads"`
	Next    []string      `json:"next"`
}

var recapClock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func validRecapOutline(outline RecapOutline) bool {
	if len(outline.Threads) > 8 || len(outline.Next) > 5 {
		return false
	}
	for _, thread := range outline.Threads {
		if strings.TrimSpace(thread.Title) == "" || len(thread.Title) > 200 || !recapClock.MatchString(thread.From) || !recapClock.MatchString(thread.To) {
			return false
		}
	}
	for _, item := range outline.Next {
		if strings.TrimSpace(item) == "" || len(item) > 300 {
			return false
		}
	}
	return true
}

// SaveMemoryRecap keeps the recap the person's computer wrote for one day in a Space's tree. Like
// a summary written by hand it is off, so no schedule picks it up, and its result is the text.
func (db *Database) SaveMemoryRecap(ctx context.Context, user, spaceID string, day time.Time, text string, outline RecapOutline) (string, error) {
	text = strings.TrimSpace(text)
	if user == "" || text == "" || len(text) > 20000 || !validRecapOutline(outline) {
		return "", ErrSpaceInvalid
	}
	stored, err := json.Marshal(outline)
	if err != nil {
		return "", err
	}
	surface := RecapSummaryPrefix + day.Format("2006-01-02")
	err = db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		space, err := memorySpaceTx(ctx, tx, user, spaceID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_recaps(user_id,space_id,surface_id,enabled,cadence,prompt,last_result,outline,last_run_at)
 VALUES($1,$2,$3,false,'daily',$4,$5,$6::jsonb,now())
 ON CONFLICT(user_id,space_id,surface_id) DO UPDATE SET prompt=EXCLUDED.prompt,last_result=EXCLUDED.last_result,outline=EXCLUDED.outline,last_run_at=now(),updated_at=now()`,
			user, space, surface, day.Format("Monday, January 2"), text, string(stored))
		return err
	})
	if err != nil {
		return "", err
	}
	return "summary:" + surface, nil
}

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
