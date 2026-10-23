import { describe, expect, it } from "vitest";
import type { ModelMessage } from "ai";
import { Ledger, ledgerFromPrompt, ledgerSuffix, withLedger } from "../src/ledger.js";
import { finishIssues } from "../src/finish-verification.js";

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
});
