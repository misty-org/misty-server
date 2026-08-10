package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var standardAgentCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type DueAgentWorkflowSchedule struct {
	InstanceID        string
	UserID            string
	SpaceID           string
	AgentID           string
	WorkflowVersionID string
	CapabilityID      string
	EventID           string
	ScheduledFor      time.Time
}

// ClaimDueAgentWorkflowSchedules evaluates schedules in each user's IANA time
// zone and atomically claims one coalesced occurrence. The event-claim primary
// key makes concurrent coordinators and reordered ticks safe.
func (db *Database) ClaimDueAgentWorkflowSchedules(ctx context.Context, now time.Time, limit int) ([]DueAgentWorkflowSchedule, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	type row struct {
		instanceID, userID, spaceID, agentID, workflowVersionID string
		config, cursor                                          json.RawMessage
		updatedAt                                               time.Time
	}
	claimed := []DueAgentWorkflowSchedule{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.user_id,i.space_id,i.agent_id,w.workflow_version_id,w.trigger_config,w.cursor,w.updated_at
			FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id
			WHERE w.enabled AND w.consent->>'granted'='true' AND w.trigger_config->>'kind'='cron'
			ORDER BY w.updated_at LIMIT $1`, limit*4)
		if err != nil {
			return err
		}
		candidates := []row{}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.instanceID, &item.userID, &item.spaceID, &item.agentID, &item.workflowVersionID, &item.config, &item.cursor, &item.updatedAt); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range candidates {
			if len(claimed) >= limit {
				break
			}
			var config struct {
				Expression   string `json:"expression"`
				Timezone     string `json:"timezone"`
				CapabilityID string `json:"capabilityId"`
			}
			var cursor struct {
				LastScheduledAt time.Time `json:"lastScheduledAt"`
			}
			if json.Unmarshal(item.config, &config) != nil || json.Unmarshal(item.cursor, &cursor) != nil || strings.TrimSpace(config.CapabilityID) == "" {
				continue
			}
			baseline := item.updatedAt
			if !cursor.LastScheduledAt.IsZero() {
				baseline = cursor.LastScheduledAt
			}
			due, ok := TestingNextAgentSchedule(config.Expression, config.Timezone, baseline, now)
			if !ok {
				continue
			}
			eventID := "cron:" + due.UTC().Format("20060102T150405Z")
			result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,state)
				VALUES($1,$2,'cron',$3,$4,'claimed') ON CONFLICT DO NOTHING`, item.instanceID, item.workflowVersionID, eventID, config.Expression+"@"+config.Timezone)
			if err != nil {
				return err
			}
			inserted, _ := result.RowsAffected()
			if inserted != 1 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_agent_instance_workflows SET cursor=jsonb_set(cursor,'{lastScheduledAt}',to_jsonb($3::text),true),updated_at=NOW() WHERE instance_id=$1 AND workflow_version_id=$2`, item.instanceID, item.workflowVersionID, due.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			claimed = append(claimed, DueAgentWorkflowSchedule{InstanceID: item.instanceID, UserID: item.userID, SpaceID: item.spaceID, AgentID: item.agentID, WorkflowVersionID: item.workflowVersionID, CapabilityID: config.CapabilityID, EventID: eventID, ScheduledFor: due.UTC()})
		}
		return nil
	})
	return claimed, err
}

func (db *Database) FinishWorkflowEventClaim(ctx context.Context, instanceID, workflowVersionID, provider, eventID, runID, state string) error {
	if state != "completed" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_workflow_event_claims SET state=$1,run_id=NULLIF($2,''),updated_at=NOW()
			WHERE instance_id=$3 AND workflow_version_id=$4 AND provider=$5 AND event_id=$6 AND state='claimed'`, state, runID, instanceID, workflowVersionID, provider, eventID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return fmt.Errorf("%w: workflow event claim", ErrSpaceInvalid)
		}
		return nil
	})
}

func (db *Database) BindWorkflowEventClaim(ctx context.Context, instanceID, workflowVersionID, provider, eventID, runID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_workflow_event_claims SET run_id=$1,updated_at=NOW()
			WHERE instance_id=$2 AND workflow_version_id=$3 AND provider=$4 AND event_id=$5 AND state='claimed'`, runID, instanceID, workflowVersionID, provider, eventID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return fmt.Errorf("%w: workflow event claim", ErrSpaceInvalid)
		}
		return nil
	})
}

func (db *Database) FinalizeWorkflowEventClaimsForRun(ctx context.Context, runID, state string) error {
	if state != "completed" && state != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_workflow_event_claims SET state=$1,updated_at=NOW() WHERE run_id=$2 AND state='claimed'`, state, runID)
		return err
	})
}

func TestingNextAgentSchedule(expression, timezone string, baseline, now time.Time) (time.Time, bool) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || strings.TrimSpace(timezone) == "" || timezone == "local" {
		return time.Time{}, false
	}
	spec, err := standardAgentCronParser.Parse(expression)
	if err != nil {
		return time.Time{}, false
	}
	next := spec.Next(baseline.In(location))
	return next, !next.After(now.In(location))
}

func TestingAgentScheduleDue(expression string, baseline, now time.Time) bool {
	spec, err := standardAgentCronParser.Parse(expression)
	return err == nil && !spec.Next(baseline).After(now)
}

func ValidAgentSchedule(expression string) bool {
	_, err := standardAgentCronParser.Parse(expression)
	return err == nil
}
