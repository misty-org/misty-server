// End-state checks for account tasks: after a run, the runner reads back what the
// task should have changed (an issue, drafts, events, a channel, Misty items) and
// the judge grades against that instead of the agent's own account of it.
import type { EvalClient } from "./client.js";
import type { ComposioCall } from "./composio.js";
import { find } from "./composio.js";

/**
 * One read after the run: a Composio tool or a Misty API path, with {{placeholders}}.
 * `since` keeps only items created or changed after the run started. `undo` runs once
 * per item the run created ({{item.field}} fills from it), so the next run starts clean;
 * a `misty` undo is "METHOD /path", and `skip` leaves items whose field matches a pattern
 * (another app's events, a join message). Items without a creation time are undone by
 * `match` instead: every item whose fields match its patterns. `{{item.id|strip:event:}}`
 * drops a prefix from a value.
 */
export interface StateCheck {
  label: string;
  composio?: string;
  args?: Record<string, unknown>;
  misty?: string;
  since?: boolean;
  undo?: { composio?: string; args?: Record<string, unknown>; misty?: string; skip?: Record<string, string>; match?: Record<string, string> };
}

type Item = Record<string, unknown>;

const zone = "America/Los_Angeles";
const perCheck = 12_000;

// Fields that carry no evidence: raw MIME, previews, API links, reactions, avatars.
const noise = new Set(["payload", "preview", "attachmentList", "labelIds", "node_id", "reactions", "avatar_url", "gravatar_id",
  "performed_via_github_app", "sub_issues_summary", "issue_dependencies_summary", "author_association", "active_lock_reason",
  "etag", "kind", "iCalUID", "sequence", "reminders", "creator", "organizer", "conferenceData", "blocks", "team", "client_msg_id"]);

/** Keeps what a judge can check (text, subjects, recipients, titles, labels, times) and drops the rest. */
export function compact(value: unknown): unknown {
  if (typeof value === "string") return value.length > 1_500 ? `${value.slice(0, 1_500)}…` : value;
  if (Array.isArray(value)) return value.map(compact).filter((item) => item !== undefined);
  if (!value || typeof value !== "object") return value ?? undefined;
  const out: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) {
    if (noise.has(key) || (key.endsWith("_url") && key !== "html_url") || key === "url" || item === null || item === "") continue;
    const kept = compact(item);
    if (kept !== undefined && !(Array.isArray(kept) && kept.length === 0)) out[key] = kept;
  }
  return Object.keys(out).length ? out : undefined;
}

// Creation and change times as items carry them: GitHub and Misty, Calendar, Slack, Gmail.
const createdFields = ["created_at", "createdAt", "created", "ts", "messageTimestamp"];
const changedFields = ["updated_at", "updatedAt", "updated"];

function time(item: Item, fields: string[]): number | undefined {
  const message = item.message && typeof item.message === "object" ? (item.message as Item) : {};
  for (const field of fields) {
    const value = item[field] ?? message[field];
    if (typeof value !== "string" && typeof value !== "number") continue;
    const ms = field === "ts" ? Number(value) * 1000 : Date.parse(String(value));
    if (Number.isFinite(ms)) return ms;
  }
  return undefined;
}

/** Runs `visit` on every list of items that carry a creation time. */
function lists(value: unknown, visit: (items: Item[]) => Item[] | void): unknown {
  if (Array.isArray(value)) {
    const items = value.filter((item): item is Item => !!item && typeof item === "object" && !Array.isArray(item));
    if (items.length && items.length === value.length && items.some((item) => time(item, createdFields) !== undefined)) return visit(items) ?? value;
    return value.map((item) => lists(item, visit));
  }
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, lists(item, visit)]));
}

// A little slack for clocks that disagree between this machine and the apps.
const skewMs = 5_000;

/** Items created after `since`, which the run made. */
function created(value: unknown, since: Date): Item[] {
  const found: Item[] = [];
  lists(value, (items) => void found.push(...items.filter((item) => (time(item, createdFields) ?? 0) >= since.getTime() - skewMs)));
  return found;
}

/** Every item, anywhere in the result, whose fields all match the patterns. */
function matching(value: unknown, patterns: Array<readonly [string, RegExp]>): Item[] {
  if (Array.isArray(value)) return value.flatMap((item) => matching(item, patterns));
  if (!value || typeof value !== "object") return [];
  const item = value as Item;
  const self = patterns.every(([field, pattern]) => pattern.test(String(item[field] ?? ""))) ? [item] : [];
  return [...self, ...Object.values(item).flatMap((child) => matching(child, patterns))];
}

/** Keeps only items created or changed after `since`. */
function recent(value: unknown, since: Date): unknown {
  return lists(value, (items) => items.filter((item) => Math.max(time(item, createdFields) ?? 0, time(item, changedFields) ?? 0) >= since.getTime() - skewMs));
}

/** Midnight in the eval's time zone, `days` from today, as an RFC 3339 time. */
function localMidnight(days: number): string {
  const date = new Date(Date.now() + days * 86_400_000).toLocaleDateString("en-CA", { timeZone: zone });
  const offset = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" }).formatToParts(new Date())
    .find((part) => part.type === "timeZoneName")?.value.replace("GMT", "") || "+00:00";
  return `${date}T00:00:00${offset}`;
}

export class StateReader {
  private values = new Map<string, string>();

  constructor(private readonly client: EvalClient, private readonly composio: ComposioCall) {}

  private async value(name: string, since: Date): Promise<string> {
    if (name === "since") return since.toISOString();
    if (name === "today_start") return localMidnight(0);
    if (name === "tomorrow_start") return localMidnight(1);
    if (name === "tomorrow_end") return localMidnight(2);
    if (name === "week_start") return localMidnight(-((new Date().getDay() + 6) % 7));
    const cached = this.values.get(name);
    if (cached) return cached;
    let found = "";
    if (name === "github_owner") found = find(await this.composio("GITHUB_GET_THE_AUTHENTICATED_USER", {}), "login");
    else if (name.startsWith("slack_channel:")) {
      const result = await this.composio("SLACK_FIND_CHANNELS", { query: name.slice("slack_channel:".length), exact_match: true });
      found = find(result, "id");
    } else if (name === "space") {
      const spaces = await this.client.json<{ spaces: Array<{ id: string }> }>("/spaces");
      found = spaces.spaces[0]?.id ?? "";
    }
    if (!found) throw new Error(`no value for {{${name}}}`);
    this.values.set(name, found);
    return found;
  }

  private async fill(text: string, since: Date, item: Item = {}): Promise<string> {
    let out = text;
    for (const match of text.matchAll(/\{\{([a-z_:.|-]+)\}\}/gi)) {
      const name = match[1]!;
      const [field = "", strip] = name.slice(5).split("|strip:");
      let value = name.startsWith("item.") ? String(item[field] ?? "") : await this.value(name, since);
      if (strip && value.startsWith(strip)) value = value.slice(strip.length);
      out = out.replace(match[0], value);
    }
    return out;
  }

  /** Arguments with placeholders filled; a whole-value {{item.x}} keeps x's own type. */
  private async args(args: Record<string, unknown> | undefined, since: Date, item: Item = {}): Promise<Record<string, unknown>> {
    const out: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(args ?? {})) {
      const whole = typeof value === "string" ? /^\{\{item\.([a-z_]+)\}\}$/i.exec(value) : null;
      out[key] = whole ? item[whole[1]!] : typeof value === "string" ? await this.fill(value, since, item) : value;
    }
    return out;
  }

  private async fetch(check: StateCheck, since: Date): Promise<unknown> {
    if (check.composio) return this.composio(check.composio, await this.args(check.args, since));
    if (check.misty) return this.client.json(await this.fill(check.misty, since));
    return undefined;
  }

  /** The task's end state as text, one section per check; failures are reported, not thrown. */
  async read(checks: StateCheck[], since: Date): Promise<string> {
    const sections: string[] = [];
    for (const check of checks) {
      try {
        let result = await this.fetch(check, since);
        if (check.since) result = recent(result, since);
        const scope = check.since ? " (created or changed during the run)" : "";
        sections.push(`## ${check.label}${scope}\n${JSON.stringify(compact(result) ?? {}).slice(0, perCheck)}`);
      } catch (error) {
        sections.push(`## ${check.label}\n(could not read: ${String(error).slice(0, 200)})`);
      }
    }
    return sections.join("\n\n");
  }

  /** Undoes what the run created (deletes drafts, events, messages and tasks; closes issues). */
  async undo(checks: StateCheck[], since: Date): Promise<string[]> {
    const failures: string[] = [];
    for (const check of checks) {
      if (!check.undo) continue;
      try {
        const skip = Object.entries(check.undo.skip ?? {}).map(([field, pattern]) => [field, new RegExp(pattern)] as const);
        const match = Object.entries(check.undo.match ?? {}).map(([field, pattern]) => [field, new RegExp(pattern)] as const);
        const result = await this.fetch(check, since);
        for (const item of match.length ? matching(result, match) : created(result, since)) {
          if (skip.some(([field, pattern]) => pattern.test(String(item[field] ?? "")))) continue;
          try {
            if (check.undo.composio) await this.composio(check.undo.composio, await this.args(check.undo.args, since, item));
            else if (check.undo.misty) {
              const [method, path] = (await this.fill(check.undo.misty, since, item)).split(" ");
              await this.client.json(path!, { method });
            }
          } catch (error) {
            failures.push(`${check.label}: ${String(error).slice(0, 160)}`);
          }
        }
      } catch (error) {
        failures.push(`${check.label}: ${String(error).slice(0, 160)}`);
      }
    }
    return failures;
  }
}
