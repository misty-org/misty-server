import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { backed, Ledger, recordRows, ledgerFromPrompt, ledgerSuffix, withLedger } from "../src/ledger.js";
import { finishIssues, reportOnlyIssue } from "../src/finish-verification.js";

const call = (name: string, input: unknown): ModelMessage => ({ role: "assistant", content: [{ type: "tool-call", toolCallId: "c", toolName: name, input }] });

describe("ledger", () => {
  it("sends a done row without evidence back to pending", () => {
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "Open site A", state: "done", evidence: { kind: "tab", ref: "https://a.example", summary: "" } }, { requirement: "Open site B", state: "done" }] });
    const unbacked = ledger.reconcile([call("browser_open", { url: "https://a.example/page" })]);
    expect(unbacked.map((row) => row.requirement)).toEqual(["Open site B"]);
    expect(ledger.remaining().map((item) => item.requirement)).toEqual(["Open site B"]);
  });

  it("keeps a carried done row without re-checking its evidence", () => {
    const ledger = new Ledger([{ id: "r1", requirement: "A", state: "done", evidence: { kind: "note", ref: "note-1", summary: "" } }]);
    expect(ledger.reconcile([])).toEqual([]);
    expect(ledger.remaining()).toEqual([]);
  });

  it("makes a completed report partial while a row is open", () => {
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "A", state: "failed", reason: "captcha" }] });
    const report = withLedger({ outcome: "completed", summary: "ok", remaining: [] }, ledger);
    expect(report.outcome).toBe("partial");
    expect(report.remaining).toEqual([{ requirement: "A", reason: "captcha" }]);
  });

  it("round-trips through the text a run ends with", () => {
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "A <b>", state: "failed", reason: "captcha", needs: "takeover" }] });
    const rows = ledgerFromPrompt(`Continue.\n${ledgerSuffix(ledger)}`);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ requirement: "A <b>", needs: "takeover" });
  });

  it("raises a finish issue for rows nothing backs", () => {
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "A", state: "done" }] });
    const unbackedRows = ledger.reconcile([]);
    const issues = finishIssues({
      request: "do A", report: { outcome: "completed", summary: "done", remaining: [] }, messages: [], turn: 1, limit: 30, long: false,
      research: {} as never, readOnly: () => false, unbackedRows,
    });
    expect(issues.join("\n")).toMatch(/marked done/);
  });

  it("sends a completed finish back while its ledger has open rows", () => {
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "Find A", state: "pending" }, { requirement: "Find B", state: "skipped", reason: "not asked" }] });
    const check = (outcome: "completed" | "partial") => finishIssues({
      request: "find A and B", report: { outcome, summary: "A is 1", remaining: [] }, messages: [], turn: 1, limit: 30, long: false,
      research: {} as never, readOnly: () => false, openRows: ledger.remaining(),
    }).join("\n");
    expect(check("completed")).toMatch(/1 part as not done \("Find A"\)/);
    expect(check("partial")).toBe("");
  });

  it("backs evidence written as a sentence around an address, and answer rows with the answer", () => {
    const shown = JSON.stringify({ tabs: [{ url: "https://stockeldpark.co.uk/plan-your-day/tickets-prices/christmas-ticket-options/" }] }).toLowerCase();
    expect(backed({ kind: "tab", ref: "https://stockeldpark.co.uk/plan-your-day/tickets-prices/christmas-ticket-options/ scrolled to Gold Package; kept()", summary: "" }, shown)).toBe(true);
    expect(backed({ kind: "tab", ref: "https://www.yorkmaze.com/plan-a-visit/ kept", summary: "" }, shown)).toBe(false);
    const searched = JSON.stringify({ results: [{ title: "HomePod plays Apple Music - Apple Support Communities", url: "https://discussions.apple.com/x?a=1&b=2" }, { title: "r/HomeKit on Reddit" }] }).toLowerCase();
    expect(backed({ kind: "note", ref: "Apple Support Communities discussion threads; Reddit r/HomeKit", summary: "" }, searched)).toBe(true);
    expect(backed({ kind: "note", ref: "Consumer Reports review of speakers", summary: "" }, searched)).toBe(false);
    const ledger = new Ledger();
    ledger.update({ rows: [{ requirement: "Compare the two", state: "done", evidence: { kind: "note", ref: "Final answer text below with comparison", summary: "" } }] });
    expect(ledger.reconcile([], "x".repeat(250))).toEqual([]);
    expect(ledger.reconcile([], "")).toHaveLength(1);
  });

  it("sends back an answer that reports the work instead of giving it", () => {
    expect(reportOnlyIssue("Completed all 26 requirements. Delivered side-by-side comparison.")).toMatch(/describes the work/);
    expect(reportOnlyIssue("## Stockeld Park vs Harrogate\n| | Stockeld | Funland |")).toBeUndefined();
  });

  it("keeps a part done without evidence out of done, and tells the run", () => {
    const ledger = new Ledger();
    recordRows(ledger, { rows: [{ requirement: "Read A", state: "done", evidence: { kind: "tab", ref: "https://a.test", summary: "" } }] });
    const { rows, missing } = recordRows(ledger, { rows: [{ requirement: "Read A", state: "done" }, { requirement: "Read B", state: "done" }] });
    expect(missing.map((row) => row.requirement)).toEqual(["Read A", "Read B"]);
    expect(rows.find((row) => row.requirement === "Read A")).toMatchObject({ state: "done", evidence: { ref: "https://a.test" } });
    expect(rows.find((row) => row.requirement === "Read B")).toMatchObject({ state: "pending" });
  });
});
