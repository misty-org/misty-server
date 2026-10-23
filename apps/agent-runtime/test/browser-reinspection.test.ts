import { expect, it } from "vitest";
import { browserReinspectionInstruction, browserReinspectionTool, requiresBrowserReinspection } from "../src/browser-reinspection.js";

it("only replans for a known browser pre-dispatch stale rejection", () => {
  expect(requiresBrowserReinspection("browser.upload", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(true);
  for (const result of [undefined, { status: "uncertain", reason: "browser_snapshot_stale" }, { denied: true }, { status: "failure", reason: "permission_denied", attempted: false }, { status: "failure", reason: "browser_snapshot_stale" }, { status: "failure", reason: "browser_snapshot_stale", attempted: true }]) {
    expect(requiresBrowserReinspection("browser.upload", result)).toBe(false);
  }
  expect(requiresBrowserReinspection("message.send", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(false);
});

it("inspects the page again after a stale upload", () => {
  expect(browserReinspectionTool("browser.upload")).toBe("browser.inspect");
  expect(browserReinspectionInstruction("browser.inspect")).toContain("browser_inspect");
});

it("refreshes the snapshot when page steps were rejected before dispatch", () => {
  const stale = { status: "failure", reason: "browser_snapshot_stale", attempted: false };
  for (const name of ["browser.steps", "browser.fill_form"]) {
    expect(requiresBrowserReinspection(name, stale)).toBe(true);
    expect(requiresBrowserReinspection(name, { ...stale, attempted: true })).toBe(false);
    expect(browserReinspectionTool(name)).toBe("browser.snapshot");
  }
  expect(browserReinspectionInstruction("browser.snapshot")).toContain("browser_snapshot");
});
