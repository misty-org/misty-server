import { expect, it } from "vitest";
import { browserReinspectionInstruction, browserReinspectionTool, requiresBrowserReinspection } from "../src/browser-reinspection.js";

it("only replans for a known browser pre-dispatch stale rejection", () => {
  expect(requiresBrowserReinspection("browser.click", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(true);
  for (const result of [undefined, { status: "uncertain", reason: "browser_snapshot_stale" }, { denied: true }, { status: "failure", reason: "permission_denied", attempted: false }, { status: "failure", reason: "browser_snapshot_stale" }, { status: "failure", reason: "browser_snapshot_stale", attempted: true }]) {
    expect(requiresBrowserReinspection("browser.click", result)).toBe(false);
  }
  expect(requiresBrowserReinspection("messages.send", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(false);
});

it("recaptures workspace input only when the host confirms no dispatch", () => {
  const stale = {status: "failure", reason: "browser_snapshot_stale", attempted: false};
  expect(requiresBrowserReinspection("browser.workspace.interact", stale)).toBe(true);
  expect(browserReinspectionTool("browser.workspace.interact")).toBe("browser.workspace.visual");
  expect(browserReinspectionTool("browser.click")).toBe("browser.inspect");
  expect(browserReinspectionInstruction("browser.workspace.visual")).toContain("browser_workspace_visual");
  for (const result of [{...stale, attempted: true}, {...stale, status: "uncertain"}, {status: "failure", reason: "permission_denied", attempted: false}]) {
    expect(requiresBrowserReinspection("browser.workspace.interact", result)).toBe(false);
  }
});
