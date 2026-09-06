import { readFile, writeFile } from "node:fs/promises";
import { mistyServerMethods, MISTY_APP_PROTOCOL_VERSION } from "@misty/contracts";

// Temporary compatibility artifact for the Go owner during staged migration.
const target = new URL("../../internal/apprpc/methods.json", import.meta.url);
const expected = JSON.stringify({ protocol: MISTY_APP_PROTOCOL_VERSION, methods: mistyServerMethods }, null, 2) + "\n";
if (process.argv.includes("--check")) {
  if (await readFile(target, "utf8") !== expected) throw new Error("Go routes differ from the packaged SDK snapshot");
} else await writeFile(target, expected);
console.log(`${Object.keys(mistyServerMethods).length} public method routes ${process.argv.includes("--check") ? "verified" : "synchronized"}.`);
