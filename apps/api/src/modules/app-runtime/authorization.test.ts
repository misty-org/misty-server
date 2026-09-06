import { expect, it } from "vitest";
import { authorizeMethod } from "./authorization.js";

it("requires every capability for calendar operations that cross domains", () => {
  expect(authorizeMethod("calendar.sync", ["calendar.write"])).toBe(false);
  expect(authorizeMethod("calendar.sync", ["calendar.write", "tasks.write"])).toBe(true);
  expect(authorizeMethod("calendar.google.calendars", ["calendar.read"])).toBe(false);
  expect(authorizeMethod("calendar.google.calendars", ["calendar.read", "connections.read"])).toBe(true);
});
it("uses task permissions for the agenda and roadmap permissions for nested mutations", () => {
  expect(authorizeMethod("agenda.list", ["tasks.read"])).toBe(true);
  expect(authorizeMethod("agenda.list", ["agenda.read"])).toBe(false);
  expect(authorizeMethod("roadmaps.goals.setTasks", ["tasks.write"])).toBe(false);
  expect(authorizeMethod("roadmaps.goals.setTasks", ["roadmaps.write"])).toBe(true);
});
