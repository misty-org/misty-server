import { expect, it } from "vitest";
import { visibleErrorMessage } from "../src/runtime-errors.js";

it("keeps a plan limit's code so the app can tell the person", () => {
  const message = "app_action_limit_reached: This account used its 1,000 app actions for this billing period.";
  expect(visibleErrorMessage(new Error(message))).toBe(message);
  expect(visibleErrorMessage(new Error("tool_execution_failed: app_action_limit_reached: done for the month"))).toBe("app_action_limit_reached: done for the month");
});
