import type { ModelMessage } from "ai";
import { expect, it } from "vitest";
import { DROPPED_SNAPSHOT, pruneStalePageSnapshots, withCacheBreakpoint } from "../src/prompt-cache.js";

const tree = (n: number) => `- button "Item ${n}" [ref=e${n}]\n`.repeat(1_000);
const result = (id: string, toolName: string, value: unknown): ModelMessage => ({
  role: "tool", content: [{ type: "tool-result", toolCallId: id, toolName, output: { type: "text", value: JSON.stringify(value) } }],
});
const call = (id: string, toolName: string): ModelMessage => ({ role: "assistant", content: [{ type: "tool-call", toolCallId: id, toolName, input: {} }] });

function transcript(snapshots: number): ModelMessage[] {
  const messages: ModelMessage[] = [{ role: "user", content: "Fill the form." }];
  for (let index = 0; index < snapshots; index++) {
    const name = index % 2 ? "browser_steps" : "browser_snapshot";
    messages.push(call(`c${index}`, name), result(`c${index}`, name, index % 2 ? { ran: 1, snapshot: { documentId: "d", tree: tree(index) } } : { documentId: "d", tree: tree(index) }));
  }
  return messages;
}

const trees = (messages: ModelMessage[]) => messages.flatMap((message) => message.role === "tool" ? message.content.map((part) => {
  const value = JSON.parse((part as { output: { value: string } }).output.value);
  return value.tree ?? value.snapshot.tree;
}) : []);

it("keeps every snapshot until the stale ones are worth a cache miss", () => {
  const short = transcript(3);
  expect(pruneStalePageSnapshots(short)).toEqual({ messages: short, dropped: 0 });
});

it("drops all but the newest two snapshots in one batch", () => {
  const long = transcript(6);
  const { messages, dropped } = pruneStalePageSnapshots(long);
  expect(dropped).toBe(4);
  const kept = trees(messages);
  expect(kept.slice(0, 4).every((value) => value === DROPPED_SNAPSHOT)).toBe(true);
  expect(kept[4]).toBe(tree(4));
  expect(kept[5]).toBe(tree(5));
  // Calls and their results stay paired; the original is untouched.
  expect(messages.length).toBe(long.length);
  expect(trees(long)[0]).toBe(tree(0));
  // Already-dropped snapshots do not trigger another rewrite.
  expect(pruneStalePageSnapshots([...messages, call("x", "browser_snapshot"), result("x", "browser_snapshot", { tree: "- text: hi" })]).dropped).toBe(0);
});

it("leaves other tools' results alone", () => {
  const messages: ModelMessage[] = [{ role: "user", content: "Go" }];
  for (let index = 0; index < 8; index++) messages.push(call(`n${index}`, "note_search"), result(`n${index}`, "note_search", { tree: tree(index) }));
  expect(pruneStalePageSnapshots(messages).dropped).toBe(0);
});

it("moves one Anthropic cache breakpoint to the newest message", () => {
  const first = withCacheBreakpoint([{ role: "user", content: "a" }, { role: "user", content: "b" }], "anthropic/claude-sonnet-5-5");
  expect(first[0]!.providerOptions).toBeUndefined();
  expect(first[1]!.providerOptions).toEqual({ anthropic: { cacheControl: { type: "ephemeral" } } });
  const second = withCacheBreakpoint([...first, { role: "user", content: "c" }], "anthropic/claude-sonnet-5-5");
  expect(second.filter((message) => message.providerOptions?.anthropic).length).toBe(1);
  expect(second[2]!.providerOptions?.anthropic).toBeDefined();
  const openai: ModelMessage[] = [{ role: "user", content: "a" }];
  expect(withCacheBreakpoint(openai, "openai/gpt-6-luna")).toBe(openai);
});
