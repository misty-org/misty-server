import { decodeHTML } from "entities";
import addresses from "email-addresses";
import libmime from "libmime";
import { parse, type DefaultTreeAdapterTypes } from "parse5";
import type { MailAddress, MailMessage, MailThread } from "@misty/contracts";

export const zeroTime = "0001-01-01T00:00:00Z";
export const cleanHeader = (value: string) => decodeHTML(value).replace(/[\u0000-\u0008\u000a-\u001f\u007f-\u009f]/g, " ").trim();
export const cleanText = (value: string) => decodeHTML(value).replace(/\r\n?/g, "\n").replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/g, "").trim();
export function decodeHeader(value: string) {
  for (const match of value.matchAll(/=\?([^?]+)\?[bq]\?[^?]*\?=/gi)) if (!["utf-8", "us-ascii", "iso-8859-1"].includes(match[1]!.toLowerCase())) return value;
  try { return libmime.decodeWords(value); } catch { return value; }
}
export function parseAddresses(value: string): MailAddress[] {
  if (!value.trim() || value.length > 65536) return [];
  try {
    const parsed = addresses.parseAddressList({ input: value, rfc6532: true, partial: false });
    return (parsed ?? []).flatMap((entry) => entry.type === "group" ? entry.addresses : [entry])
      .filter((entry) => !!entry.address.trim()).map((entry) => ({ name: cleanHeader(decodeHeader(entry.name ?? "")), email: entry.address.trim() }));
  } catch { return []; }
}

const ignoredTags = new Set(["script", "style", "iframe", "object", "embed", "form", "svg", "math"]);
const blockTags = new Set(["p", "div", "br", "li", "tr", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6"]);
export function htmlToText(value: string) {
  const stack: Array<DefaultTreeAdapterTypes.Node | string> = [parse(value)], output: string[] = [];
  while (stack.length) {
    const node = stack.pop()!;
    if (typeof node === "string") { output.push(node); continue; }
    if ("tagName" in node && ignoredTags.has(node.tagName)) continue;
    if (node.nodeName === "#text" && "value" in node) { output.push(node.value); continue; }
    if ("tagName" in node && blockTags.has(node.tagName)) { output.push("\n"); stack.push("\n"); }
    const children = "content" in node ? node.content.childNodes : "childNodes" in node ? node.childNodes : [];
    for (let index = children.length - 1; index >= 0; index--) stack.push(children[index]!);
  }
  return output.join("").split(/[\s\u0085]+/u).filter(Boolean).join(" ");
}

function calendarDate(year: number, month: number, day: number, hour: number, minute: number, second: number): Date | null {
  const date = new Date(0); date.setUTCFullYear(year, month - 1, day); date.setUTCHours(hour, minute, second, 0);
  return date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day && date.getUTCHours() === hour && date.getUTCMinutes() === minute && date.getUTCSeconds() === second ? date : null;
}
const canonical = (date: Date) => Number.isFinite(date.getTime()) && date.getUTCFullYear() >= 0 && date.getUTCFullYear() <= 9999 ? date.toISOString().replace(/\.000Z$/, "Z") : zeroTime;
export function graphTime(...values: string[]) {
  for (const value of values) {
    const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
    if (!match || !calendarDate(+match[1]!, +match[2]!, +match[3]!, +match[4]!, +match[5]!, +match[6]!)) continue;
    const date = new Date(value); if (!Number.isFinite(date.getTime())) continue;
    const fraction = (match[7] ?? "").slice(0, 9).replace(/0+$/, "");
    const stamp = canonical(date).replace(/\.\d+Z$/, "Z");
    return stamp === zeroTime ? stamp : stamp.replace(/Z$/, `${fraction ? `.${fraction}` : ""}Z`);
  }
  return zeroTime;
}
const months = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
const zones: Record<string, number> = { UT: 0, GMT: 0, UTC: 0, EST: -300, EDT: -240, CST: -360, CDT: -300, MST: -420, MDT: -360, PST: -480, PDT: -420 };
export function gmailTime(header: string, internal: string) {
  const match = /^(?:[A-Za-z]{3},\s*)?(\d{1,2})\s+([A-Za-z]{3})\s+(\d{2}|\d{4})\s+(\d{2}):(\d{2})(?::(\d{2}))?\s+([+-]\d{4}|[A-Za-z]{1,5})(?:\s*\([^)]*\))?$/.exec(header.trim());
  if (match) {
    let year = +match[3]!; if (match[3]!.length === 2) year += year >= 69 ? 1900 : 2000;
    const date = calendarDate(year, months.indexOf(match[2]!.toLowerCase()) + 1, +match[1]!, +match[4]!, +match[5]!, +(match[6] ?? 0));
    const zone = match[7]!, numeric = /^[+-]/.test(zone), hour = numeric ? +zone.slice(1, 3) : 0, minute = numeric ? +zone.slice(3) : 0;
    if (date && hour <= 23 && minute <= 59) {
      const offset = numeric ? (hour * 60 + minute) * (zone[0] === "+" ? 1 : -1) : Object.hasOwn(zones, zone) ? zones[zone]! : 0;
      return canonical(new Date(date.getTime() - offset * 60000));
    }
  }
  if (!/^\+?\d+$/.test(internal)) return zeroTime;
  const milliseconds = Number(internal); return Number.isSafeInteger(milliseconds) && milliseconds >= 0 ? canonical(new Date(milliseconds)) : zeroTime;
}
const timeKey = (value: string) => value.replace(/(?:\.(\d+))?Z$/, (_match, fraction: string | undefined) => `.${(fraction ?? "").padEnd(9, "0")}`);
export const compareTime = (a: string, b: string) => timeKey(a) < timeKey(b) ? -1 : timeKey(a) > timeKey(b) ? 1 : 0;
export const sortLabels = (labels: string[]) => labels.sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
export function groupThread(provider: string, accountId: string, id: string, messages: MailMessage[], snippet?: string): MailThread {
  const thread: MailThread = { provider, account_id: accountId, provider_id: id, subject: "", snippet: snippet ?? "", participants: [], messages,
    labels: [], last_message_at: zeroTime, unread: false, starred: false };
  const labels = new Set<string>(), participants = new Set<string>();
  for (const message of messages) {
    if (!thread.subject && message.subject) thread.subject = message.subject;
    if (compareTime(message.sent_at, thread.last_message_at) > 0) { thread.last_message_at = message.sent_at; if (snippet === undefined) thread.snippet = message.snippet; }
    thread.unread ||= message.unread; thread.starred ||= message.starred;
    message.labels.forEach((label) => labels.add(label));
    for (const address of [message.from, ...message.cc]) {
      const key = address.email.toLowerCase(); if (key && !participants.has(key)) { participants.add(key); thread.participants.push(address); }
    }
  }
  thread.labels = sortLabels([...labels]); messages.sort((a, b) => compareTime(a.sent_at, b.sent_at)); return thread;
}
