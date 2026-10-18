// Seeds the account-backed workflow tasks into the eval runner's connected test
// apps, through the same Composio session Misty's agent uses. Nothing is sent:
// mail is inserted into the mailbox, invites go out to no one, and every name and
// address is fictional. Run once, from server/apps/agent-runtime:
//
//   npx jiti evals/workflows/seed.ts
//
// Mail carries the "Misty eval" label and events say "Misty eval seed", for cleanup.
import { EvalClient } from "./client.js";
import { composioFor, find } from "./composio.js";

const zone = "America/Los_Angeles";
const client = await EvalClient.open();
const run = await composioFor(client);

// --- Gmail -------------------------------------------------------------------
const labels = await run("GMAIL_LIST_LABELS", { user_id: "me" });
let labelId = JSON.stringify(labels).match(/"id":"(Label_[^"]+)","name":"Misty eval"/)?.[1] ?? "";
if (!labelId) labelId = find(await run("GMAIL_CREATE_LABEL", { user_id: "me", label_name: "Misty eval" }), "id");

interface Mail { from: string; to: string; subject: string; date: string; body: string; sent?: boolean }
const mine = "me@misty-eval.example";
const mails: Mail[] = [
  { from: "Katherine Johnson <katherine@example.com>", to: mine, date: "2026-10-15T09:12:00-07:00",
    subject: "Bug: Export to CSV crashes on empty projects",
    body: "Hi,\n\nExporting a project with no tasks crashes the app.\n\nSteps to reproduce:\n1. Create a new project and don't add any tasks.\n2. Open the project and choose Export > CSV.\n3. The app shows a blank screen.\n\nThe console says: TypeError: Cannot read properties of undefined (reading 'map').\nExpected: a CSV with just the header row.\n\nChrome 141 on macOS 15.\n\nThanks,\nKatherine" },
  { from: "Northwind Supplies <orders@northwind.example>", to: mine, date: "2026-10-03T08:00:00-07:00",
    subject: "Your order #4821 has shipped", body: "Order #4821 (2 x monitor arm) shipped today with Parcelways, tracking PW-48210093." },
  { from: "Kestrel Office <hello@kestrel.example>", to: mine, date: "2026-10-01T10:30:00-07:00",
    subject: "Order #5190 is on its way", body: "Good news: order #5190 (desk chair) has shipped. Tracking: KO-5190-7781." },
  { from: "Brightline Paper <shipping@brightline.example>", to: mine, date: "2026-10-06T11:00:00-07:00",
    subject: "Order #7730 shipped", body: "Your order #7730 (printer paper, 10 reams) has shipped." },
  { from: "Brightline Paper <shipping@brightline.example>", to: mine, date: "2026-10-09T15:20:00-07:00",
    subject: "Order #7730 delivered", body: "Your order #7730 was delivered and signed for at the front desk." },
  { from: "Acme Cables <ship@acme-cables.example>", to: mine, date: "2026-10-13T09:45:00-07:00",
    subject: "Order #9112 has shipped", body: "Order #9112 (USB-C cables, 20 pack) shipped. Expected delivery: Oct 18." },
  { from: mine, to: "Priya Nair <priya.nair@example.com>", date: "2026-10-13T16:00:00-07:00", sent: true,
    subject: "Thanks for interviewing with us", body: "Hi Priya,\n\nThanks for taking the time to talk with us today. We'll be in touch next week.\n\nBest" },
  { from: "Sam Lee <sam@example.com>", to: mine, date: "2026-10-16T08:05:00-07:00",
    subject: "Q4 budget review", body: "Hey, can you review the Q4 budget sheet and send me your comments by Friday? Thanks!" },
  { from: "Ada <ada@example.org>", to: mine, date: "2026-10-16T09:40:00-07:00",
    subject: "Offsite dates?", body: "Quick question: are we still doing the offsite on Nov 6-7, or did it move? Need to book travel." },
  { from: "This Week in AI Agents <news@digest.example>", to: mine, date: "2026-10-16T06:00:00-07:00",
    subject: "This week in AI agents: browser agents grow up", body: "Top stories this week... You're receiving this because you subscribed." },
  { from: "Streamly <receipts@streamly.example>", to: mine, date: "2026-10-16T07:15:00-07:00",
    subject: "Your Streamly receipt", body: "Thanks for your payment of $9.99 for Streamly Premium. No action needed." },
];

const messageId = (mail: Mail) => `${mail.date.replace(/\W/g, "")}.${mail.subject.length}@misty-eval.example`;

function raw(mail: Mail): string {
  const message = [
    `From: ${mail.from}`, `To: ${mail.to}`, `Subject: ${mail.subject}`, `Date: ${new Date(mail.date).toUTCString()}`,
    `Message-ID: <${messageId(mail)}>`,
    "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8", "", mail.body,
  ].join("\r\n");
  return Buffer.from(message).toString("base64url");
}

for (const mail of mails) {
  // Seeding twice must not duplicate mail.
  const existing = await run("GMAIL_FETCH_EMAILS", { user_id: "me", query: `rfc822msgid:${messageId(mail)}`, max_results: 1 });
  if (find(existing, "messageId") || find(existing, "id")) {
    console.log(`mail: ${mail.subject} (already there)`);
    continue;
  }
  const inserted = await run("GMAIL_INSERT_MESSAGE", { user_id: "me", raw: raw(mail), internalDateSource: "dateHeader" });
  const id = find(inserted, "id");
  // Only real sending adds SENT; an inserted "sent" message is found by its sender and recipient.
  const add = mail.sent ? [labelId] : ["INBOX", "UNREAD", labelId];
  await run("GMAIL_ADD_LABEL_TO_EMAIL", { user_id: "me", message_id: id, add_label_ids: add });
  console.log(`mail: ${mail.subject}`);
}

// --- Google Calendar ---------------------------------------------------------
const events = [
  { summary: "Interview: Priya Nair", start: "2026-10-13T10:00:00", hours: 1, attendee: "priya.nair@example.com" },
  { summary: "Interview: Diego Alvarez", start: "2026-10-14T14:00:00", hours: 1, attendee: "diego.alvarez@example.com" },
  { summary: "Interview: Mei Chen", start: "2026-10-15T11:00:00", hours: 1, attendee: "mei.chen@example.com" },
  { summary: "Team lunch and planning", start: "2026-10-17T12:00:00", hours: 2 },
  { summary: "Design review", start: "2026-10-17T14:30:00", hours: 1 },
];
for (const event of events) {
  // A read-only Calendar connection can't create events; say so and seed the rest.
  const created = await run("GOOGLECALENDAR_CREATE_EVENT", {
    calendar_id: "primary", summary: event.summary, start_datetime: event.start, event_duration_hour: event.hours,
    timezone: zone, description: "Misty eval seed", send_updates: "none", create_meeting_room: false,
    ...(event.attendee ? { attendees: [event.attendee] } : {}),
  }).catch((error: unknown) => {
    if (!/insufficient authentication scopes/i.test(String(error))) throw error;
    return undefined;
  });
  if (!created) {
    console.log("events: the Calendar connection can't create events (read-only scopes); add them by hand or reconnect with write access");
    break;
  }
  console.log(`event: ${event.summary}`);
}

// --- GitHub and Slack --------------------------------------------------------
try {
  await run("GITHUB_CREATE_A_REPOSITORY_FOR_THE_AUTHENTICATED_USER", {
    name: "misty-eval-sandbox", private: true, auto_init: true, has_issues: true,
    description: "Sandbox for Misty's workflow evals. Safe to delete.",
  });
  console.log("repo: misty-eval-sandbox");
} catch (error) {
  if (!/already exists|name already/i.test(String(error))) throw error;
  console.log("repo: misty-eval-sandbox already exists");
}
try {
  await run("SLACK_CREATE_CHANNEL", { name: "marketing-drafts" });
  console.log("slack: #marketing-drafts");
} catch (error) {
  if (!/name_taken/i.test(String(error))) throw error;
  console.log("slack: #marketing-drafts already exists");
}

// --- Misty note --------------------------------------------------------------
// A note's body changes only through collaboration, so the agent's own note tool fills it in.
const spaces = await client.json<{ spaces: Array<{ id: string; name: string }> }>("/spaces");
const space = spaces.spaces[0];
if (!space) throw new Error("No space to hold the Weekly sync note.");
const notes = await client.json<{ notes?: Array<{ title?: string }> }>(`/spaces/${space.id}/notes`);
if ((notes.notes ?? []).some((note) => note.title === "Weekly sync")) {
  console.log(`note: Weekly sync in ${space.name} (already there)`);
  process.exit(0);
}
await client.json(`/spaces/${space.id}/notes`, { method: "POST", body: JSON.stringify({ title: "Weekly sync" }) });
const filled = await client.run({
  mode: "drawer", surface_id: "global", model_override: "anthropic/claude-haiku-5.5", reasoning_effort: "low",
  prompt: `In my note "Weekly sync" in the ${space.name} space, replace the body with exactly these three lines and nothing else:\nAction items:\n- Ada: send the offsite agenda by Monday\n- Sam: finish the Q4 budget review\n- Me: book the venue for Nov 6`,
});
console.log(`note: Weekly sync in ${space.name} (${filled.terminal}, ${filled.tools.join(", ")})`);
