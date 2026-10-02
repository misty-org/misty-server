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

const consoleJobStateCounts = `
	SELECT 'Scheduled tasks', state, count(*) FROM scheduled_tasks WHERE enabled GROUP BY state
	UNION ALL
	SELECT 'AI invocations (24h)', state, count(*) FROM ai_invocations
		WHERE created_at > now() - interval '1 day' GROUP BY state
	UNION ALL
	SELECT 'Agent runs (24h)', state, count(*) FROM agent_run_jobs
		WHERE updated_at > now() - interval '1 day' GROUP BY state
	UNION ALL
	SELECT 'Library reindex', status, count(*) FROM smart_library_reindex_jobs
		WHERE status IN ('pending','processing') OR updated_at > now() - interval '1 day' GROUP BY status
	ORDER BY 1, 2`

const consoleJobAttention = `
	(SELECT 'Scheduled task', t.title, COALESCE(u.email, t.user_id), t.state,
	        CASE WHEN t.state = 'failed' THEN t.last_error ELSE 'next run ' || COALESCE(to_char(t.next_run_at, 'YYYY-MM-DD HH24:MI'), 'unscheduled') END,
	        t.updated_at
	 FROM scheduled_tasks t LEFT JOIN users u ON u.id = t.user_id
	 WHERE t.enabled AND t.state IN ('running','failed'))
	UNION ALL
	(SELECT 'AI invocation', i.mode || ' · ' || i.surface_id, COALESCE(u.email, i.user_id), i.state,
	        i.error_code, i.updated_at
	 FROM ai_invocations i LEFT JOIN users u ON u.id = i.user_id
	 WHERE i.state NOT IN ('completed','failed','canceled')
	    OR (i.state = 'failed' AND i.updated_at > now() - interval '1 day'))
	UNION ALL
	(SELECT 'Agent run', j.agent_id, COALESCE(j.space_id, ''), j.state,
	        j.last_error_message, j.updated_at
	 FROM agent_run_jobs j
	 WHERE j.state IN ('queued','leased','dispatched')
	    OR (j.state = 'failed' AND j.updated_at > now() - interval '1 day'))
	UNION ALL
	(SELECT 'Library reindex', r.embedding_model, COALESCE(u.email, r.user_id), r.status,
	        r.completed_assets || '/' || r.requested_assets || ' assets, ' || r.failed_assets || ' failed',
	        r.updated_at
	 FROM smart_library_reindex_jobs r LEFT JOIN users u ON u.id = r.user_id
	 WHERE r.status IN ('pending','processing')
	    OR (r.status IN ('failed','partially_failed') AND r.updated_at > now() - interval '1 day'))
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

// ConsoleAIUsage summarizes the last day of AI invocations by state and the
// most common failure codes.
func (db *Database) ConsoleAIUsage(ctx context.Context) (states, failures []ConsoleStateCount, err error) {
	err = db.consoleTx(ctx, func(tx *sql.Tx) error {
		if states, err = queryStateCounts(ctx, tx, `
			SELECT mode, state, count(*) FROM ai_invocations
			WHERE created_at > now() - interval '1 day' GROUP BY mode, state ORDER BY 1, 2`); err != nil {
			return err
		}
		failures, err = queryStateCounts(ctx, tx, `
			SELECT 'failed', COALESCE(NULLIF(error_code, ''), 'unknown'), count(*) FROM ai_invocations
			WHERE state = 'failed' AND created_at > now() - interval '1 day'
			GROUP BY 2 ORDER BY 3 DESC LIMIT 8`)
		return err
	})
	return states, failures, err
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
