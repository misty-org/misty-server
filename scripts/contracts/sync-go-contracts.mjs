import { z } from "zod";
import { readFile, writeFile } from "node:fs/promises";
import { mistyServerMethods, MISTY_APP_PROTOCOL_VERSION, mistyInboxCapabilities, mistySocialCapabilities, mistyTaskCapabilities, MistyBrowserInteractionSchema, MistyRoutineDefinitionSchema } from "@misty/contracts";

// Generate Go dispatch routes from the reviewed public SDK contract snapshot.
const target = new URL("../../internal/apprpc/methods.json", import.meta.url);
const expected = JSON.stringify({ protocol: MISTY_APP_PROTOCOL_VERSION, methods: mistyServerMethods }, null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(target, "utf8") !== expected) throw new Error("Go routes differ from the packaged SDK snapshot");
} else await writeFile(target, expected);
console.log(`${Object.keys(mistyServerMethods).length} public method routes ${process.argv.includes("--check") ? "verified" : "synchronized"}.`);

// Reserve Misty-owned semantic definitions before any downloaded provider can
// claim them. This is the same SDK data, not a second handwritten tool catalog.
const capabilityTarget = new URL("../../internal/capabilities/builtins.json", import.meta.url);
const capabilityExpected = JSON.stringify([...mistyInboxCapabilities, ...mistySocialCapabilities, ...mistyTaskCapabilities], null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(capabilityTarget, "utf8") !== capabilityExpected) throw new Error("Go capability contracts differ from the packaged SDK snapshot");
} else await writeFile(capabilityTarget, capabilityExpected);

// The runtime driver uses the SDK interaction vocabulary, not a parallel schema.
const browserTarget = new URL("../../internal/capabilities/browser-interaction.json", import.meta.url);
const browserExpected = JSON.stringify(z.toJSONSchema(MistyBrowserInteractionSchema), null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(browserTarget, "utf8") !== browserExpected) throw new Error("Browser interaction schema differs from the SDK snapshot");
} else await writeFile(browserTarget, browserExpected);

// Go validates saved routines against the same structural contract. Reference
// ordering, timezone and aggregate-budget refinements are also enforced in Go.
const routineTarget = new URL("../../internal/capabilities/routine-definition.json", import.meta.url);
const routineExpected = JSON.stringify(z.toJSONSchema(MistyRoutineDefinitionSchema), null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(routineTarget, "utf8") !== routineExpected) throw new Error("Routine schema differs from the SDK snapshot");
} else await writeFile(routineTarget, routineExpected);

// Shared acceptance fixtures exercise refinements absent from JSON Schema as
// well as SDK defaults. Go consumes these same SDK-evaluated examples.
const pin = { capability: "habits.list", capabilityVersion: 1, providerId: "example.habits/backend", providerVersion: 1, targetId: "10000000-0000-4000-8000-000000000001", targetRevision: 1 };
const literal = value => ({ kind: "literal", value });
const ref = stepId => ({ kind: "reference", source: { kind: "step", stepId }, path: [] });
const draft = () => ({ protocol: 1, name: "Daily habits", trigger: { kind: "manual" }, budget: {}, steps: [{ id: "read", label: "Read habits", kind: "capability", action: pin, input: literal({}) }] });
const examples = [
  ["defaults", draft()],
  ["prior_reference", { ...draft(), steps: [...draft().steps, { ...draft().steps[0], id: "save", input: ref("read") }] }],
  ["forward_reference", { ...draft(), steps: [{ ...draft().steps[0], input: ref("future") }] }],
  ["duplicate_step", { ...draft(), steps: [...draft().steps, ...draft().steps] }],
  ["literal_is_data", { ...draft(), steps: [{ ...draft().steps[0], input: literal(ref("future")) }] }],
  ["missing_property_condition", { ...draft(), steps: [{ ...draft().steps[0], when: { kind: "exists", reference: { kind: "reference", source: { kind: "trigger" }, path: ["available"] } } }] }],
  ["oversized_fields", { ...draft(), steps: [{ ...draft().steps[0], input: { kind: "object", fields: Object.fromEntries(Array.from({ length: 101 }, (_, i) => [`field${i}`, literal(i)])) } }] }],
  ["bad_path", { ...draft(), steps: [{ ...draft().steps[0], input: { ...ref("read"), path: ["constructor"] } }] }],
  ["agent_budget", { ...draft(), budget: { modelTurns: 1 }, steps: [{ id: "summarize", label: "Summarize", kind: "agent", prompt: literal("Summarize"), actions: [pin], maxTurns: 2, outputSchema: { type: "object" } }] }],
  ["app_event_defaults", { ...draft(), trigger: { kind: "app_event", event: "journal.note.changed" } }],
  ["timezone", { ...draft(), trigger: { kind: "schedule", timezone: "America/Los_Angeles", daysOfWeek: [1, 2, 3], times: [{ hour: 9, minute: 0 }] } }],
  ["bad_timezone", { ...draft(), trigger: { kind: "schedule", timezone: "Local", daysOfWeek: [1], times: [{ hour: 9, minute: 0 }] } }],
  ["duplicate_schedule", { ...draft(), trigger: { kind: "schedule", timezone: "UTC", daysOfWeek: [1, 1], times: [{ hour: 9, minute: 0 }] } }],
  ["unknown_field", { ...draft(), enabled: true }],
  ["null_default", { ...draft(), description: null }],
];
const routineFixtureTarget = new URL("../../internal/capabilities/routine-conformance.json", import.meta.url);
const routineFixtureExpected = JSON.stringify(examples.map(([name, input]) => {
  const parsed = MistyRoutineDefinitionSchema.safeParse(input);
  return { name, input, accepted: parsed.success, ...(parsed.success ? { normalized: parsed.data } : {}) };
}), null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(routineFixtureTarget, "utf8") !== routineFixtureExpected) throw new Error("Routine fixtures differ from the SDK snapshot");
} else await writeFile(routineFixtureTarget, routineFixtureExpected);
