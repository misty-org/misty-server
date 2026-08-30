package db

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

type SpaceAgendaEntry struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	AllDay          bool      `json:"all_day"`
	Timezone        string    `json:"timezone"`
	Status          string    `json:"status,omitempty"`
	SourceID        string    `json:"source_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	RoadmapID       string    `json:"roadmap_id,omitempty"`
	MilestoneID     string    `json:"milestone_id,omitempty"`
	GoalID          string    `json:"goal_id,omitempty"`
	RoadmapNodeID   string    `json:"roadmap_node_id,omitempty"`
	RoadmapNodeKind string    `json:"roadmap_node_kind,omitempty"`
	DefinitionID    string    `json:"definition_id,omitempty"`
	MeetingURL      string    `json:"meeting_url,omitempty"`
	Location        string    `json:"location,omitempty"`
	ExternalEventID string    `json:"external_event_id,omitempty"`
	Version         int64     `json:"version,omitempty"`
}

type SpaceAgendaSnapshot struct {
	Entries []SpaceAgendaEntry `json:"entries"`
}

func (db *Database) SpaceAgenda(ctx context.Context, userID, spaceID string, from, to time.Time) (SpaceAgendaSnapshot, error) {
	out := SpaceAgendaSnapshot{Entries: []SpaceAgendaEntry{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		linkedEvents, err := loadSpaceAgendaTasksTx(ctx, tx, userID, spaceID, from, to, &out)
		if err != nil {
			return err
		}
		if err := loadSpaceAgendaCalendarTx(ctx, tx, spaceID, from, to, linkedEvents, &out); err != nil {
			return err
		}
		if err := loadSpaceAgendaNativeCalendarTx(ctx, tx, userID, spaceID, from, to, &out); err != nil {
			return err
		}
		return loadSpaceAgendaRoadmapDatesTx(ctx, tx, userID, spaceID, from, to, &out)
	})
	sort.SliceStable(out.Entries, func(i, j int) bool {
		if out.Entries[i].StartsAt.Equal(out.Entries[j].StartsAt) {
			return out.Entries[i].Kind < out.Entries[j].Kind
		}
		return out.Entries[i].StartsAt.Before(out.Entries[j].StartsAt)
	})
	return out, err
}

func loadSpaceAgendaTasksTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, from, to time.Time, out *SpaceAgendaSnapshot) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE space_id=$1 AND archived_at IS NULL AND status<>'canceled' AND (
		(due_at >= $2 AND due_at < $3)
	) AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY due_at,id`, spaceID, from, to, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	linked := map[string]bool{}
	for rows.Next() {
		var task SpaceTask
		if err := scanSpaceTask(rows, &task); err != nil {
			return nil, err
		}
		entry := SpaceAgendaEntry{ID: "task:" + task.ID, Kind: "task", TaskID: task.ID, Title: task.Title, Description: task.Notes, Timezone: task.DueTimezone, Status: task.Status}
		if task.DueAt != nil {
			entry.StartsAt, entry.EndsAt = *task.DueAt, task.DueAt.Add(30*time.Minute)
		}
		if entry.StartsAt.IsZero() {
			continue
		}
		out.Entries = append(out.Entries, entry)
	}
	return linked, rows.Err()
}

func loadSpaceAgendaCalendarTx(ctx context.Context, tx *sql.Tx, spaceID string, from, to time.Time, linked map[string]bool, out *SpaceAgendaSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,source_id,external_event_id,title,description,location,meeting_url,starts_at,ends_at,all_day,timezone,status FROM space_calendar_events WHERE space_id=$1 AND starts_at<$2 AND ends_at>$3 AND removed_at IS NULL AND status<>'canceled' ORDER BY starts_at,id`, spaceID, to, from)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item SpaceAgendaEntry
		item.Kind = "event"
		if err := rows.Scan(&item.ID, &item.SourceID, &item.ExternalEventID, &item.Title, &item.Description, &item.Location, &item.MeetingURL, &item.StartsAt, &item.EndsAt, &item.AllDay, &item.Timezone, &item.Status); err != nil {
			return err
		}
		if linked[item.SourceID+"\x00"+item.ExternalEventID] {
			continue
		}
		item.ID = "event:" + item.ID
		out.Entries = append(out.Entries, item)
	}
	return rows.Err()
}

func loadSpaceAgendaNativeCalendarTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, from, to time.Time, out *SpaceAgendaSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,title,description,location,starts_at,ends_at,all_day,timezone,status,version FROM space_native_calendar_events WHERE space_id=$1 AND starts_at<$2 AND ends_at>$3 AND archived_at IS NULL AND status<>'canceled' AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY starts_at,id`, spaceID, to, from, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item SpaceAgendaEntry
		item.Kind, item.SourceID = "event", "misty"
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Location, &item.StartsAt, &item.EndsAt, &item.AllDay, &item.Timezone, &item.Status, &item.Version); err != nil {
			return err
		}
		item.ID = "event:" + item.ID
		out.Entries = append(out.Entries, item)
	}
	return rows.Err()
}

func loadSpaceAgendaRoadmapDatesTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, from, to time.Time, out *SpaceAgendaSnapshot) error {
	fromDate, toDate := from.Format("2006-01-02"), to.Format("2006-01-02")
	milestones, err := tx.QueryContext(ctx, `SELECT m.id,m.roadmap_id,m.title,m.description,m.target_date FROM space_roadmap_milestones m JOIN space_roadmaps r ON r.id=m.roadmap_id WHERE m.space_id=$1 AND m.archived_at IS NULL AND r.archived_at IS NULL AND m.target_date >= $2::date AND m.target_date < $3::date AND (r.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=r.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY m.target_date,m.id`, spaceID, fromDate, toDate, userID)
	if err != nil {
		return err
	}
	for milestones.Next() {
		var item SpaceAgendaEntry
		var date time.Time
		item.Kind, item.AllDay, item.Timezone = "milestone", true, "UTC"
		if err := milestones.Scan(&item.MilestoneID, &item.RoadmapID, &item.Title, &item.Description, &date); err != nil {
			milestones.Close()
			return err
		}
		item.ID, item.StartsAt, item.EndsAt = "milestone:"+item.MilestoneID, date, date.Add(24*time.Hour)
		out.Entries = append(out.Entries, item)
	}
	if err := milestones.Close(); err != nil {
		return err
	}
	goals, err := tx.QueryContext(ctx, `SELECT g.id,g.roadmap_id,g.milestone_id,g.title,g.description,g.target_date FROM space_roadmap_goals g JOIN space_roadmaps r ON r.id=g.roadmap_id JOIN space_roadmap_milestones m ON m.id=g.milestone_id WHERE g.space_id=$1 AND g.archived_at IS NULL AND m.archived_at IS NULL AND r.archived_at IS NULL AND g.target_date >= $2::date AND g.target_date < $3::date AND (r.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=r.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY g.target_date,g.id`, spaceID, fromDate, toDate, userID)
	if err != nil {
		return err
	}
	for goals.Next() {
		var item SpaceAgendaEntry
		var date time.Time
		item.Kind, item.AllDay, item.Timezone = "goal", true, "UTC"
		if err := goals.Scan(&item.GoalID, &item.RoadmapID, &item.MilestoneID, &item.Title, &item.Description, &date); err != nil {
			return err
		}
		item.ID, item.StartsAt, item.EndsAt = "goal:"+item.GoalID, date, date.Add(24*time.Hour)
		out.Entries = append(out.Entries, item)
	}
	if err := goals.Err(); err != nil {
		goals.Close()
		return err
	}
	if err := goals.Close(); err != nil {
		return err
	}
	nodes, err := tx.QueryContext(ctx, `SELECT n.id,n.roadmap_id,n.node_kind,COALESCE(n.definition_id,''),n.title,n.description,n.target_date FROM space_roadmap_nodes n JOIN space_roadmaps r ON r.id=n.roadmap_id LEFT JOIN space_roadmap_node_definitions d ON d.id=n.definition_id WHERE n.space_id=$1 AND n.archived_at IS NULL AND r.archived_at IS NULL AND (n.milestone_id IS NULL OR EXISTS(SELECT 1 FROM space_roadmap_milestones m WHERE m.id=n.milestone_id AND m.archived_at IS NULL)) AND n.target_date >= $2::date AND n.target_date < $3::date AND (n.node_kind IN ('risk','decision','metric') OR (n.node_kind='custom' AND d.agenda_visible=TRUE)) AND (r.audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=r.audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$4)) ORDER BY n.target_date,n.id`, spaceID, fromDate, toDate, userID)
	if err != nil {
		return err
	}
	defer nodes.Close()
	for nodes.Next() {
		var item SpaceAgendaEntry
		var date time.Time
		item.Kind, item.AllDay, item.Timezone = "roadmap_node", true, "UTC"
		if err := nodes.Scan(&item.RoadmapNodeID, &item.RoadmapID, &item.RoadmapNodeKind, &item.DefinitionID, &item.Title, &item.Description, &date); err != nil {
			return err
		}
		item.ID, item.StartsAt, item.EndsAt = "roadmap_node:"+item.RoadmapNodeID, date, date.Add(24*time.Hour)
		out.Entries = append(out.Entries, item)
	}
	return nodes.Err()
}
