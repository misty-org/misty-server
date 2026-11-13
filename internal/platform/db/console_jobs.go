package db

import (
	"context"
	"database/sql"
	"time"
)

type ConsoleStateCount struct {
	Kind  string
	State string
	Count int
}

// ConsoleJob is one background job that needs an operator's attention:
// running, waiting or recently failed.
type ConsoleJob struct {
	Kind      string
	Title     string
	Owner     string
	State     string
	Detail    string
	UpdatedAt time.Time
}

// Only the Library's own jobs still run on the server: every agent, schedule and
// model call happens on the person's computer.
const consoleJobStateCounts = `
	SELECT 'Library jobs (24h)', state, count(*) FROM library_processing_jobs
		WHERE updated_at > now() - interval '1 day' GROUP BY state
	ORDER BY 1, 2`

const consoleJobAttention = `
	SELECT 'Library job', j.job_kind, COALESCE(j.space_id, ''), j.state, COALESCE(j.error_code, ''), j.updated_at
	FROM library_processing_jobs j
	WHERE j.state IN ('queued','leased','running')
	   OR (j.state = 'failed' AND j.updated_at > now() - interval '1 day')
	ORDER BY 6 DESC LIMIT $1`

func (db *Database) ConsoleJobs(ctx context.Context, limit int) ([]ConsoleStateCount, []ConsoleJob, error) {
	var counts []ConsoleStateCount
	var jobs []ConsoleJob
	err := db.consoleTx(ctx, func(tx *sql.Tx) error {
		var err error
		if counts, err = queryStateCounts(ctx, tx, consoleJobStateCounts); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, consoleJobAttention, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j ConsoleJob
			if err := rows.Scan(&j.Kind, &j.Title, &j.Owner, &j.State, &j.Detail, &j.UpdatedAt); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	})
	return counts, jobs, err
}

func queryStateCounts(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]ConsoleStateCount, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var counts []ConsoleStateCount
	for rows.Next() {
		var c ConsoleStateCount
		if err := rows.Scan(&c.Kind, &c.State, &c.Count); err != nil {
			return nil, err
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}
