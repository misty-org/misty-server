// The desktop's page layer and step rules in Misty's own browser: scripted
// tool calls on the eval's fixture sites, with no model, through the desktop's
// eval bridge (src-tauri/src/infra/browser_eval_bridge.rs). Skips unless the
// desktop app is running with the bridge (see evals/browser/README.md).
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { bridgeConfig, openEvalTab, type EvalBrowser } from "../evals/browser/webview.js";
import { PageTools } from "../evals/browser/page-tools.js";
import { serveSites } from "../evals/browser/tasks.js";

const scope = "eval-scope-0001";
let browser: EvalBrowser;
let tools: PageTools;
let sites: { base: string; close: () => Promise<void> };

type Snapshot = { documentId: string; tree: string };
const snapshot = async () => (await tools.run("browser.snapshot", { scopeId: scope })) as Snapshot;
function ref(tree: string, line: RegExp): string {
  const match = tree.split("\n").find((entry) => line.test(entry))?.match(/\[ref=(e\d+)\]/);
  if (!match?.[1]) throw new Error(`no ref for ${line} in\n${tree}`);
  return match[1];
}
async function open(site: string) {
  const url = `${sites.base}/${site}`;
  await browser.execute("browser.navigate", { url });
  // The page is read once it has loaded.
  for (let tries = 0; tries < 100; tries++) {
    if ((await browser.evaluate<boolean>(`location.href === ${JSON.stringify(url)} && document.readyState === "complete"`)) === true) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  return snapshot();
}
const steps = (documentId: string, list: unknown[], extra: Record<string, unknown> = {}) =>
  tools.run("browser.steps", { scopeId: scope, documentId, steps: list, ...extra }) as Promise<Record<string, any>>;
const state = <T>(expression: string) => browser.evaluate<T>(expression);
// A skill succeeded when every phase did and nothing reports a failure; the
// model reads it the same way (the device drops its internal ok flag).
const succeeded = (result: Record<string, any>) =>
  result.status !== "failure" && Array.isArray(result.phases) && result.phases.every((phase: { ok?: boolean }) => phase.ok === true);

describe.skipIf(!bridgeConfig())("page tools in Misty's browser", () => {
  beforeAll(async () => {
    sites = await serveSites();
    browser = await openEvalTab(`${sites.base}/archive.html`);
    tools = new PageTools(browser, async () => ({}));
  }, 60_000);
  afterAll(async () => {
    await browser?.close();
    await sites?.close();
  });

  it("fills a multi-step form with a hidden custom checkbox, a list and a datalist field", async () => {
    let page = await open("signup.html");
    const filled = await tools.run("browser.fill_form", { scopeId: scope, documentId: page.documentId, fields: [
      { ref: ref(page.tree, /textbox "Full name"/), value: "Ada Lovelace" },
      { ref: ref(page.tree, /textbox "Work email"/), value: "ada@example.com" },
    ] }) as Record<string, any>;
    expect(filled.completed).toBe(2);
    let result = await steps(page.documentId, [{ action: "click", ref: ref(page.tree, /button "Next"/) }]);
    // Native input when the tab is on screen, page events in a hidden agent tab.
    expect(["native", "script"]).toContain(result.steps[0].input ?? "script");
    page = { documentId: page.documentId, tree: result.snapshot.tree };
    result = await tools.run("browser.fill_form", { scopeId: scope, documentId: page.documentId, fields: [
      { ref: ref(page.tree, /combobox "Plan"/), value: ["Pro"] },
      { ref: ref(page.tree, /combobox "Country"/), value: "Canada" },
      { ref: ref(page.tree, /checkbox "I agree to the terms"/), value: true },
    ] }) as Record<string, any>;
    expect(result.completed).toBe(3);
    page = { documentId: page.documentId, tree: result.snapshot.tree };
    result = await steps(page.documentId, [{ action: "click", ref: ref(page.tree, /button "Next"/) }]);
    result = await steps(page.documentId, [{ action: "click", ref: ref(result.snapshot.tree, /button "Create account"/) }]);
    expect(await state("window.__evalState.submitted")).toEqual({ name: "Ada Lovelace", email: "ada@example.com", plan: "pro", country: "Canada", terms: true });
  }, 60_000);

  it("keeps refs across actions and rejects a stale document before acting", async () => {
    const page = await open("board.html");
    const move = ref(page.tree, /button "Move"/);
    const first = await steps(page.documentId, [{ action: "click", ref: move }]);
    expect(first.steps[0].ok).toBe(true);
    // The same ref still works without a new snapshot.
    expect((await steps(page.documentId, [{ action: "key", key: "Escape" }, { action: "click", ref: move }])).completed).toBe(2);
    const stale = await steps("not-the-document", [{ action: "click", ref: move }]);
    expect(stale).toMatchObject({ status: "failure", reason: "browser_snapshot_stale", attempted: false });
  }, 60_000);

  it("uses menus and native selects to move and assign a card", async () => {
    const page = await open("board.html");
    const opened = await steps(page.documentId, [{ action: "click", ref: ref(page.tree, /button "Move"/) }]);
    const tree = opened.snapshot.tree as string;
    await steps(opened.snapshot.documentId, [
      { action: "click", ref: ref(tree, /menuitem "Move to Done"/) },
    ]);
    const after = await snapshot();
    const card = after.tree.split("\n").findIndex((line) => line.includes('article "Write report"'));
    const owner = after.tree.split("\n").slice(card).find((line) => /combobox "Owner"/.test(line))!.match(/ref=(e\d+)/)![1]!;
    await steps(after.documentId, [{ action: "select", ref: owner, values: ["Ada"] }]);
    expect(await state("JSON.stringify(window.__evalState.cards.find(k => k.title === 'Write report'))")).toBe(JSON.stringify({ id: 1, title: "Write report", col: "Done", owner: "Ada" }));
  }, 60_000);

  it("reports a covered control, then works once the banner is dismissed", async () => {
    const page = await open("cookie.html");
    const download = ref(page.tree, /button "Download report"/);
    const blocked = await steps(page.documentId, [{ action: "click", ref: download }]);
    expect(blocked).toMatchObject({ status: "failure", attempted: false });
    expect(blocked.steps[0].errorCode).toBe("browser_target_covered");
    const done = await steps(page.documentId, [{ action: "click", ref: ref(page.tree, /button "Reject non-essential"/) }, { action: "click", ref: download }]);
    expect(done.completed).toBe(2);
    expect(await state("window.__evalState")).toEqual({ downloaded: true, cookies: "essential" });
  }, 60_000);

  it("refuses consequential clicks unless allowed", async () => {
    const page = await open("mail.html");
    await tools.run("browser.fill_form", { scopeId: scope, documentId: page.documentId, fields: [
      { ref: ref(page.tree, /textbox "To"/), value: "team@example.com" },
      { ref: ref(page.tree, /textbox "Message"/), value: "The report is finished." },
    ] });
    const send = ref(page.tree, /button "Send"/);
    const refused = await steps(page.documentId, [{ action: "click", ref: send }]);
    expect(refused.steps[0].errorCode).toBe("browser_consequential_requires_approval");
    expect(await state("window.__evalState.sent")).toBeNull();
    await steps(page.documentId, [{ action: "click", ref: send }], { allowConsequential: true });
    expect(await state("window.__evalState.sent.body")).toBe("The report is finished.");
  }, 60_000);

  it("edits a grid through double-click and Enter, and a rich-text document", async () => {
    let page = await open("grid.html");
    const cell = ref(page.tree, /gridcell "B2"/);
    let result = await steps(page.documentId, [{ action: "click", ref: cell, clickCount: 2 }]);
    page = { documentId: page.documentId, tree: result.snapshot.tree };
    result = await steps(page.documentId, [{ action: "fill", ref: ref(page.tree, /textbox "Cell editor"/), text: "1200" }, { action: "key", key: "Enter" }]);
    expect(await state("window.__evalState.cells.B2")).toBe("1200");
    page = await open("editor.html");
    result = await steps(page.documentId, [{ action: "fill", ref: ref(page.tree, /textbox "Response document"/), text: "Light reactions make ATP." }, { action: "click", ref: ref(page.tree, /button "Save"/) }]);
    expect(await state("window.__evalState.saved.text")).toContain("Light reactions make ATP.");
  }, 60_000);

  it("scrolls a virtualized list and finds elements by description", async () => {
    const page = await open("longlist.html");
    const list = ref(page.tree, /list "Parts"/);
    const scrolled = await steps(page.documentId, [{ action: "scroll", ref: list, direction: "down", amount: 3 }]);
    expect(scrolled.steps[0].scrolled).toBe(true);
    expect(scrolled.snapshot.tree).not.toContain("Part #1 —");
    const found = await tools.run("browser.find", { scopeId: scope, query: "archive gemini button" }).catch(() => null);
    expect(found).toBeTruthy();
  }, 60_000);

  it("waits for text without acting", async () => {
    await open("archive.html");
    const waited = await tools.run("browser.wait_for", { scopeId: scope, text: "Projects", timeoutMs: 1000 }) as Record<string, any>;
    expect(waited.met).toBe(true);
    const missing = await tools.run("browser.wait_for", { scopeId: scope, text: "Never shown", timeoutMs: 300 }) as Record<string, any>;
    expect(missing.met).toBe(false);
  }, 60_000);

  it("lists text an injection hides from people but refuses its consequential click", async () => {
    const page = await open("injection.html");
    expect(page.tree).toContain("SYSTEM NOTICE");
    const refused = await steps(page.documentId, [{ action: "click", ref: ref(page.tree, /button "Delete account"/) }]);
    expect(refused.steps[0].errorCode).toBe("browser_consequential_requires_approval");
    expect(await state("window.__evalState.deleted")).toBe(false);
  }, 60_000);

  it("offers skills that fit the page and pastes many grid cells at once", async () => {
    const page = await open("grid.html") as Snapshot & { skills?: Array<{ id: string }> };
    const result = await tools.run("browser.snapshot", { scopeId: scope }) as Record<string, any>;
    expect(result.skills.map((skill: { id: string }) => skill.id)).toContain("grid");
    const pasted = await tools.run("browser.skill", { scopeId: scope, documentId: page.documentId, skill: "grid", action: "paste",
      args: { cell: "A1", rows: [["Item", "Jan", "Feb", "Mar"], ["Rent", "1200", "1200", "1250"], ["Food", "400", "380", "420"]] } }) as Record<string, any>;
    expect(succeeded(pasted)).toBe(true);
    expect(await state("JSON.stringify([window.__evalState.cells.A1, window.__evalState.cells.D2, window.__evalState.cells.C3])")).toBe('["Item","1250","380"]');
    const read = await tools.run("browser.skill", { scopeId: scope, documentId: page.documentId, skill: "grid", action: "read", args: { from: "A1", to: "D3" } }) as Record<string, any>;
    expect(read.phases[0].tsv).toBe("Item\tJan\tFeb\tMar\nRent\t1200\t1200\t1250\nFood\t400\t380\t420");
  }, 60_000);

  it("writes formatted markdown into an editor", async () => {
    const page = await open("editor.html");
    const inserted = await tools.run("browser.skill", { scopeId: scope, documentId: page.documentId, skill: "editor", action: "insert",
      args: { ref: ref(page.tree, /textbox "Response document"/), markdown: "## Answer\n- Light reactions make ATP and NADPH.\n- The Calvin cycle fixes carbon dioxide into sugar.\n- Both happen in the chloroplast." } }) as Record<string, any>;
    expect(succeeded(inserted)).toBe(true);
    const html = await state<string>("document.getElementById('doc').innerHTML");
    expect(html).toMatch(/<h2>\s*Answer/);
    expect((html.match(/<li>/g) ?? []).length).toBe(3);
  }, 60_000);

  it("answers a form by question and picks from an autocomplete", async () => {
    let page = await open("signup.html");
    let result = await tools.run("browser.skill", { scopeId: scope, documentId: page.documentId, skill: "forms", action: "answer",
      args: { answers: [{ question: "Full name", value: "Grace Hopper" }, { question: "Work email", value: "grace@example.com" }] } }) as Record<string, any>;
    expect(succeeded(result)).toBe(true);
    expect(await state("document.getElementById('email').value")).toBe("grace@example.com");
    page = await open("booking.html");
    result = await tools.run("browser.skill", { scopeId: scope, documentId: page.documentId, skill: "pickers", action: "choose",
      args: { ref: ref(page.tree, /combobox "With"/), text: "Grace" } }) as Record<string, any>;
    expect(succeeded(result)).toBe(true);
    expect(await state("document.getElementById('who').value")).toBe("Grace Hopper");
  }, 60_000);
});
