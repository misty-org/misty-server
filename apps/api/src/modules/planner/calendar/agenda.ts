import type { PoolClient } from "pg";
import { clientInteger } from "../../spaces/model.js";
import { taskAudience } from "../access.js";
import { calendarAudience } from "./model.js";

/** Sort timestamps in PostgreSQL, retaining precision across every entry type. */
export async function loadAgenda(tx: PoolClient, spaceId: string, userId: string, from: string, to: string) {
  const rows = (await tx.query<{ payload: Record<string, unknown>; starts_at: Date; ends_at: Date }>(`WITH entries AS (
    SELECT t.due_at AS starts_at,t.due_at+interval '30 minutes' AS ends_at,1 AS source_order,t.id AS item_id,
      jsonb_build_object('id','task:'||t.id,'kind','task','task_id',t.id,'title',t.title,'description',t.notes,
        'all_day',false,'timezone',t.due_timezone,'status',t.status) AS payload
    FROM space_tasks t WHERE t.space_id=$1 AND t.archived_at IS NULL AND t.status<>'canceled'
      AND t.due_at>=$3::timestamptz AND t.due_at<$2::timestamptz AND ${taskAudience("$4")}
    UNION ALL
    SELECT e.starts_at,e.ends_at,2,e.id,jsonb_build_object('id','event:'||e.id,'kind','event','source_id',e.source_id,
      'external_event_id',e.external_event_id,'title',e.title,'description',e.description,'location',e.location,
      'meeting_url',e.meeting_url,'all_day',e.all_day,'timezone',e.timezone,'status',e.status)
    FROM space_calendar_events e WHERE e.space_id=$1 AND e.starts_at<$2::timestamptz AND e.ends_at>$3::timestamptz
      AND e.removed_at IS NULL AND e.status<>'canceled'
    UNION ALL
    SELECT e.starts_at,e.ends_at,3,e.id,jsonb_build_object('id','event:'||e.id,'kind','event','source_id','misty',
      'title',e.title,'description',e.description,'location',e.location,'all_day',e.all_day,'timezone',e.timezone,
      'status',e.status,'version',e.version::text)
    FROM space_native_calendar_events e WHERE e.space_id=$1 AND e.starts_at<$2::timestamptz AND e.ends_at>$3::timestamptz
      AND e.archived_at IS NULL AND e.status<>'canceled' AND ${calendarAudience("e")}
    UNION ALL
    SELECT m.target_date::timestamp AT TIME ZONE 'UTC',(m.target_date+1)::timestamp AT TIME ZONE 'UTC',4,m.id,
      jsonb_build_object('id','milestone:'||m.id,'kind','milestone','milestone_id',m.id,'roadmap_id',m.roadmap_id,
        'title',m.title,'description',m.description,'all_day',true,'timezone','UTC')
    FROM space_roadmap_milestones m JOIN space_roadmaps r ON r.id=m.roadmap_id
    WHERE m.space_id=$1 AND m.archived_at IS NULL AND r.archived_at IS NULL
      AND m.target_date>=$5::date AND m.target_date<$6::date AND ${calendarAudience("r")}
    UNION ALL
    SELECT g.target_date::timestamp AT TIME ZONE 'UTC',(g.target_date+1)::timestamp AT TIME ZONE 'UTC',5,g.id,
      jsonb_build_object('id','goal:'||g.id,'kind','goal','goal_id',g.id,'milestone_id',g.milestone_id,'roadmap_id',g.roadmap_id,
        'title',g.title,'description',g.description,'all_day',true,'timezone','UTC')
    FROM space_roadmap_goals g JOIN space_roadmaps r ON r.id=g.roadmap_id JOIN space_roadmap_milestones m ON m.id=g.milestone_id
    WHERE g.space_id=$1 AND g.archived_at IS NULL AND r.archived_at IS NULL AND m.archived_at IS NULL
      AND g.target_date>=$5::date AND g.target_date<$6::date AND ${calendarAudience("r")}
    UNION ALL
    SELECT n.target_date::timestamp AT TIME ZONE 'UTC',(n.target_date+1)::timestamp AT TIME ZONE 'UTC',6,n.id,
      jsonb_build_object('id','roadmap_node:'||n.id,'kind','roadmap_node','roadmap_node_id',n.id,'roadmap_id',n.roadmap_id,
        'roadmap_node_kind',n.node_kind,'definition_id',n.definition_id,'title',n.title,'description',n.description,'all_day',true,'timezone','UTC')
    FROM space_roadmap_nodes n JOIN space_roadmaps r ON r.id=n.roadmap_id LEFT JOIN space_roadmap_node_definitions d ON d.id=n.definition_id
    WHERE n.space_id=$1 AND n.archived_at IS NULL AND r.archived_at IS NULL
      AND (n.milestone_id IS NULL OR EXISTS(SELECT 1 FROM space_roadmap_milestones m WHERE m.id=n.milestone_id AND m.archived_at IS NULL))
      AND n.target_date>=$5::date AND n.target_date<$6::date
      AND (n.node_kind IN ('risk','decision','metric') OR n.node_kind='custom' AND d.agenda_visible) AND ${calendarAudience("r")}
  ) SELECT payload,starts_at,ends_at FROM entries ORDER BY starts_at,payload->>'kind',source_order,item_id`,
  [spaceId, to, from, userId, from.slice(0, 10), to.slice(0, 10)])).rows;
  return { entries: rows.map(row => {
    const entry = { ...row.payload, starts_at: row.starts_at, ends_at: row.ends_at } as Record<string, unknown>;
    for (const key of Object.keys(entry)) if (entry[key] === null || entry[key] === "" && !["title", "timezone"].includes(key)) delete entry[key];
    if (entry.version) entry.version = clientInteger(entry.version as string);
    return entry;
  }) };
}
