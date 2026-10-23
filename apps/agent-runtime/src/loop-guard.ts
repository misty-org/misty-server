import type { ModelMessage } from "ai";

// The loop guard (docs/agents/harness.md, Completion). Runs spent minutes repeating one
// call: the same page opened again and again, the same fetch of a page that didn't answer.
// browser-use breaks loops after three steps on one page without progress. Here a note to change
// approach comes, once per pattern, when:
// - one address is opened three times in the run (not only the last dozen calls: a run that went
//   back to work after answering refetched pages it had read long before);
// - one call is made three times with the same input in the last dozen, or fails twice;
// - most of the last pages opened are on one site (five hypebeast.com articles in a row, after an
//   answer that said one date was unconfirmed, October 20).

const WINDOW = 12;
const REPEATS = 3;
const FAILURES = 2;
/** Of the last SITE_WINDOW pages opened, SITE_REPEATS on one site is a loop on that site. */
const SITE_WINDOW = 8;
const SITE_REPEATS = 6;
const marker = "Loop guard";

/** Tools that open an address; the address is what repeats. */
const opening = new Set(["browser_navigate", "screen_open", "web_fetch", "browser_tab_open"]);
/** Query parameters that change nothing on the page. */
const tracking = /^(utm_\w+|fbclid|gclid|mc_cid|mc_eid|ref|ref_src|igshid|si)$/i;
/** Search engines: many result pages on one is how searching works, not a loop. */
const searchSite = /(^|\.)(google|bing|duckduckgo|yahoo|ecosia|startpage|kagi)\.[a-z.]+$/i;

type Part = { type?: string; toolCallId?: string; toolName?: string; input?: unknown; output?: unknown };

interface Call {
  name: string;
  input: unknown;
  failed: boolean;
}

function failed(output: unknown): boolean {
  if (!output || typeof output !== "object") return false;
  const value = output as { type?: string; value?: unknown };
  if (typeof value.type === "string" && value.type.startsWith("error")) return true;
  const body = value.value && typeof value.value === "object" ? (value.value as Record<string, unknown>) : {};
  return body.successful === false || typeof body.error === "string";
}

/** Every tool call in the transcript, oldest first, with whether it failed. */
function allCalls(messages: ModelMessage[]): Call[] {
  const parts = messages.flatMap((message) => (Array.isArray(message.content) ? (message.content as Part[]) : []));
  const failures = new Set(parts.filter((part) => part.type === "tool-result" && failed(part.output)).map((part) => part.toolCallId));
  return parts
    .filter((part) => part.type === "tool-call" && part.toolName)
    .map((part) => ({ name: part.toolName!, input: part.input, failed: Boolean(part.toolCallId) && failures.has(part.toolCallId) }));
}

/** The address a call opens, the same however it was written, or "". */
export function address(input: unknown): string {
  const url = (input as { url?: unknown } | undefined)?.url;
  if (typeof url !== "string") return "";
  try {
    const parsed = new URL(/^[a-z][\w+.-]*:/i.test(url) ? url : `https://${url}`);
    parsed.hash = "";
    if (parsed.protocol === "http:") parsed.protocol = "https:";
    parsed.hostname = parsed.hostname.toLowerCase().replace(/^www\./, "");
    for (const key of [...parsed.searchParams.keys()]) if (tracking.test(key)) parsed.searchParams.delete(key);
    return parsed.href.replace(/\/$/, "").replace(/\/\?/, "?");
  } catch {
    return url.trim();
  }
}

function site(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return "";
  }
}

interface Loop {
  key: string;
  text: string;
}

function loops(calls: Call[]): Loop[] {
  const found: Loop[] = [];
  const opened = calls.map((call) => (opening.has(call.name) ? address(call.input) : "")).filter(Boolean);
  // One address, three times in the run.
  const perAddress = new Map<string, number>();
  for (const url of opened) perAddress.set(url, (perAddress.get(url) ?? 0) + 1);
  for (const [url, count] of perAddress)
    if (count >= REPEATS) found.push({ key: `open:${url}`, text: `you have opened ${url} ${count} times in this run. Its content won't change by opening it again` });
  // One call, the same input, three times or failing twice, in the last dozen.
  const recent = calls.slice(-WINDOW);
  const perCall = new Map<string, { count: number; failures: number; name: string }>();
  for (const call of recent) {
    if (opening.has(call.name) && address(call.input)) continue;
    const key = `${call.name}:${JSON.stringify(call.input ?? {})}`;
    const entry = perCall.get(key) ?? { count: 0, failures: 0, name: call.name };
    entry.count++;
    if (call.failed) entry.failures++;
    perCall.set(key, entry);
  }
  for (const [key, { count, failures, name }] of perCall) {
    if (failures >= FAILURES) found.push({ key: `failed:${key}`, text: `${name} with the same input has failed ${failures} times in your last ${WINDOW} steps. It will fail the same way again` });
    else if (count >= REPEATS) found.push({ key, text: `you have called ${name} with the same input ${count} times in your last ${WINDOW} steps without moving on. Repeating it won't change the result` });
  }
  // Most of the last pages opened on one site.
  const latest = opened.slice(-SITE_WINDOW);
  if (latest.length >= SITE_REPEATS) {
    const perSite = new Map<string, number>();
    for (const url of latest) perSite.set(site(url), (perSite.get(site(url)) ?? 0) + 1);
    for (const [host, count] of perSite)
      if (host && !searchSite.test(host) && count >= SITE_REPEATS)
        found.push({ key: `site:${host}`, text: `${count} of the last ${latest.length} pages you opened are on ${host}. If they aren't adding what is still missing, more pages there won't either` });
  }
  return found;
}

/** The note for a loop the run is in, once per pattern, or undefined. */
export function loopNote(messages: ModelMessage[]): string | undefined {
  for (const { key, text } of loops(allCalls(messages))) {
    const tag = `${marker} [${key.slice(0, 160)}]`;
    const said = messages.some(
      (message) => message.role === "user" && typeof message.content === "string" && message.content.startsWith(tag),
    );
    if (said) continue;
    return `${tag}: ${text}. Change approach: use the page itself (click through, search on the site, apply its filters), try another source, or record what you have, say plainly what is unconfirmed, and move to the next part or finish.`;
  }
  return undefined;
}
