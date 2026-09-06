import type { PoolClient } from "pg";
import { spacePermissions } from "../../spaces/permissions.js";
import type { CalendarSource } from "./source-model.js";
import type { GoogleEvent } from "./google-client.js";

export async function claimCalendarWorkflows(tx: PoolClient, source: CalendarSource, event: GoogleEvent, fingerprint: string) {
  const eventId = `${event.id}:${fingerprint.slice(0, 16)}`;
  const targets = (await tx.query(`SELECT i.id,i.user_id,i.agent_id,w.workflow_version_id,w.trigger_config->>'capabilityId' AS capability_id
    FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id
    WHERE i.space_id=$1 AND w.enabled AND w.consent->>'granted'='true'
      AND w.trigger_config->>'kind' IN ('connector_event','provider_event') AND w.trigger_config->>'provider'='google'
      AND (COALESCE(w.trigger_config->>'resourceId','')='' OR w.trigger_config->>'resourceId'=$2)
      AND jsonb_typeof(w.trigger_config->'capabilityId')='string' AND w.trigger_config->>'capabilityId'<>''
    ORDER BY w.updated_at,i.id LIMIT 400 FOR SHARE OF i,w`, [source.space_id, source.external_calendar_id])).rows;
  let claimed = 0;
  for (const target of targets) {
    if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [target.user_id])).rowCount) continue;
    const member = (await tx.query<{ role: string }>("SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR SHARE", [source.space_id, target.user_id])).rows[0];
    if (!member) continue;
    const permissions: Record<string, boolean> = await spacePermissions(tx, target.user_id, { id: source.space_id, role: member.role, is_default: false });
    if (!permissions["agents.run"]) continue;
    const request = { requesting_member_id: target.user_id, space_id: source.space_id, agent_id: target.agent_id,
      source_type: "connector", capability_id: target.capability_id, trigger_kind: "connector_event",
      input: { trigger: { kind: "connector_event", provider: "google", eventId, resourceId: source.external_calendar_id, fingerprint }, event } };
    const result = await tx.query(`INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,state,execution_owner,native_request)
      VALUES($1,$2,'google',$3,$4,'claimed','hono',$5::jsonb) ON CONFLICT DO NOTHING`, [target.id, target.workflow_version_id, eventId, fingerprint, JSON.stringify(request)]);
    if (result.rowCount && ++claimed === 200) break;
  }
}
