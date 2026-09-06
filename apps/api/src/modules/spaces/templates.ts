import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { SpaceError, trimSpace } from "./model.js";
type Template = { id: string; name: string; description: string; providers: string[]; tasks: string[]; title: string; markdown: string; collections: string[] };
const templates: Template[] = [
  { id: "blank", name: "Blank Space", description: "Start with a clean Space.", providers: [], tasks: [], title: "", markdown: "", collections: [] },
  { id: "student-project", name: "Student Project", description: "Organize a class project from brief to delivery.", providers: ["google", "notion"], tasks: ["Agree on the goal", "Divide responsibilities", "Set the first deadline"], title: "Project brief", markdown: "# Project brief\n\n## Objective\n\n## Requirements\n\n## Roles\n\n## Sources\n", collections: ["Research", "Drafts", "Final Deliverables"] },
  { id: "startup", name: "Startup", description: "Keep an early team aligned around customers and outcomes.", providers: ["google", "notion"], tasks: ["Define this week's outcome", "Talk to a first user", "Assign owners"], title: "Company snapshot", markdown: "# Company snapshot\n\n## Problem\n\n## Customer\n\n## Solution\n\n## Milestone\n", collections: ["Product", "Customer Research", "Brand & Pitch"] },
  { id: "research", name: "Research", description: "Collect sources, coordinate work, and track outputs.", providers: ["google", "notion"], tasks: ["Write the research question", "Collect key sources", "Set the next checkpoint"], title: "Research plan", markdown: "# Research plan\n\n## Question\n\n## Hypothesis\n\n## Method\n\n## Responsibilities\n", collections: ["Papers", "Data", "Outputs"] },
  { id: "game-development", name: "Game Development", description: "Coordinate a small game team around the next playable build.", providers: ["discord"], tasks: ["Define a playable milestone", "Assign core roles", "Schedule a playtest"], title: "Game brief", markdown: "# Game brief\n\n## Premise\n\n## Player loop\n\n## Art direction\n\n## Milestone\n", collections: ["Art", "Audio", "Builds & References"] },
  { id: "creative-team", name: "Creative Team", description: "Move a shared brief through review and delivery.", providers: ["discord", "notion"], tasks: ["Agree on the brief", "Assign initial deliverables", "Set a review date"], title: "Creative brief", markdown: "# Creative brief\n\n## Goal\n\n## Audience\n\n## Tone\n\n## Deliverables\n\n## References\n", collections: ["Briefs", "Inspiration", "Work in Progress", "Final"] },
];
export function findTemplate(id: string) { const template = templates.find((item) => item.id === (trimSpace(id) || "blank")); if (!template) throw new SpaceError("invalid_request"); return template; }
export function listTemplates() { return templates.map((t) => ({ id: t.id, name: t.name, description: t.description, version: 1, recommended_integrations: [...t.providers], seed_summary: { task_count: t.tasks.length, note_count: t.title ? 1 : 0, collection_count: t.collections.length } })); }
export function normalizeProviders(providers: string[]) {
  const result = [...new Set(providers.map((value) => trimSpace(value).toLowerCase()))].sort();
  if (result.some((value) => !["google", "discord", "notion"].includes(value))) throw new SpaceError("invalid_request"); return result;
}
export async function spaceEvent(tx: PoolClient, spaceId: string, userId: string, type: string, entityId: string, payload: object) {
  const row = (await tx.query<{ id: string }>("INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload) VALUES($1,$2,$3,NULLIF($4,''),$5::jsonb) RETURNING id", [spaceId, type, userId, entityId, JSON.stringify(payload)])).rows[0]!;
  await tx.query("SELECT pg_notify('misty_space_events',$1)", [row.id]);
}
export async function seedTemplate(tx: PoolClient, spaceId: string, userId: string, template: Template) {
  if (template.tasks.length) await tx.query("INSERT INTO space_task_counters(space_id,last_number) VALUES($1,$2)", [spaceId, template.tasks.length]);
  for (const [index, title] of template.tasks.entries()) await tx.query(`INSERT INTO space_tasks(id,space_id,task_number,task_key,title,status,priority,rank,due_timezone,source_refs,created_by_user_id)
    VALUES($1,$2,$3,$4,$5,'todo','medium',$6,'UTC','[]'::jsonb,$7)`, [`task_${randomUUID()}`, spaceId, index + 1, `MST-${index + 1}`, title, (index + 1) * 1024, userId]);
  for (const name of template.collections) await tx.query("INSERT INTO space_albums(id,space_id,name,description,created_by_user_id) VALUES($1,$2,$3,'',$4)", [`album_${randomUUID()}`, spaceId, name, userId]);
  if (template.title) {
    const id = `note_${randomUUID()}`;
    await tx.query("INSERT INTO space_notes(id,space_id,creator_user_id,title_projection,plain_text_projection) VALUES($1,$2,$3,$4,$5)", [id, spaceId, userId, template.title, template.markdown.replaceAll("#", "").trim()]);
    await tx.query("INSERT INTO space_note_control_outbox(id,note_id,command,payload) VALUES($1,$2,'bootstrap',$3::jsonb)", [`notectl_${randomUUID()}`, id, JSON.stringify({ markdown: template.markdown, title: template.title })]);
    await spaceEvent(tx, spaceId, userId, "note.created", id, { note_id: id });
  }
}
