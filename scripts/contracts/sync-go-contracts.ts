import { z } from "zod";
import { readFile, writeFile } from "node:fs/promises";
import { mistyInboxCapabilities, mistySocialCapabilities } from "../../../src/shared/contracts/communications-capabilities.ts";
import { mistyTaskCapabilities } from "../../../src/shared/contracts/task-capabilities.ts";
import { MistyBrowserInteractionSchema } from "../../../src/shared/contracts/browser.ts";

// Reserve Misty-owned semantic definitions before any provider can
// claim them. This is the same internal contract data, not a second handwritten tool catalog.
const capabilityTarget = new URL("../../internal/capabilities/builtins.json", import.meta.url);
const capabilityExpected = JSON.stringify([...mistyInboxCapabilities, ...mistySocialCapabilities, ...mistyTaskCapabilities], null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(capabilityTarget, "utf8") !== capabilityExpected) throw new Error("Go capability contracts differ from the internal contract source");
} else await writeFile(capabilityTarget, capabilityExpected);

// The runtime driver uses the internal interaction vocabulary, not a parallel schema.
const browserTarget = new URL("../../internal/capabilities/browser-interaction.json", import.meta.url);
const browserExpected = JSON.stringify(z.toJSONSchema(MistyBrowserInteractionSchema), null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(browserTarget, "utf8") !== browserExpected) throw new Error("Browser interaction schema differs from the internal contract source");
} else await writeFile(browserTarget, browserExpected);

