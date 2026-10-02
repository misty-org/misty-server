package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// A page is bounded by rows and serialized bytes, except that one oversized
// existing event is returned alone so a reader can always make progress.
const AIInvocationPageBytes = 256 << 10
const AIInvocationPageEvents = 128

type AIInvocationEventPage struct {
	Events    []AIInvocationEventRecord
	State     string
	Head      int64
	ExpiresAt time.Time
}

func (db *Database) ReadAIInvocationEventPage(ctx context.Context, user, id string, after int64) (AIInvocationEventPage, error) {
	page := AIInvocationEventPage{Events: []AIInvocationEventRecord{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		// Capture state and head together, then constrain the page to this head.
		// A terminal snapshot cannot omit an event committed with that transition.
		err := tx.QueryRowContext(ctx, `SELECT state,expires_at,COALESCE((SELECT max(sequence) FROM ai_invocation_events WHERE invocation_id=i.id),0)
   FROM ai_invocations i WHERE id=$1 AND user_id=$2 AND expires_at>clock_timestamp()`, id, user).Scan(&page.State, &page.ExpiresAt, &page.Head)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `WITH candidates AS MATERIALIZED (
   SELECT sequence,event_type,jsonb_set(payload,'{id}',to_jsonb(sequence::text),true) AS payload,created_at FROM ai_invocation_events
   WHERE invocation_id=$1 AND sequence>$2 AND sequence<=$3 ORDER BY sequence LIMIT $4
  ), sized AS (
   SELECT *,row_number() OVER(ORDER BY sequence) AS ordinal,
    sum(octet_length(payload::text)) OVER(ORDER BY sequence) AS bytes FROM candidates
  ) SELECT sequence,event_type,payload,created_at
   FROM sized WHERE ordinal=1 OR bytes<=$5 ORDER BY sequence`, id, max(after, 0), page.Head, AIInvocationPageEvents, AIInvocationPageBytes)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIInvocationEventRecord
			if err := rows.Scan(&item.Sequence, &item.EventType, &item.Payload, &item.CreatedAt); err != nil {
				return err
			}
			page.Events = append(page.Events, item)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return page, err
}

const invocationEventPrefix = "invocation-event:"

func invocationEventTopic(topic string) bool { return validDigestTopic(topic, invocationEventPrefix) }
func (db *Database) SubscribeAIInvocationEvents(ctx context.Context, id string) (<-chan struct{}, func(), error) {
	digest := sha256.Sum256([]byte(id))
	return db.subscribeWorkerTopic(ctx, invocationEventPrefix+hex.EncodeToString(digest[:]))
}
