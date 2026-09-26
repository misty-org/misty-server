import { expect, it } from "vitest";
import { requiresBrowserReinspection } from "../src/browser-reinspection.js";

it("only replans for a known browser pre-dispatch stale rejection", () => {
  expect(requiresBrowserReinspection("browser.click", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(true);
  for (const result of [undefined, { status: "uncertain", reason: "browser_snapshot_stale" }, { denied: true }, { status: "failure", reason: "permission_denied", attempted: false }, { status: "failure", reason: "browser_snapshot_stale" }, { status: "failure", reason: "browser_snapshot_stale", attempted: true }]) {
    expect(requiresBrowserReinspection("browser.click", result)).toBe(false);
  }
  expect(requiresBrowserReinspection("messages.send", { status: "failure", reason: "browser_snapshot_stale", attempted: false })).toBe(false);
});
