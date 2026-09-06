import { readFile } from "node:fs/promises";
import { expect, it } from "vitest";
import { requestFingerprint } from "./create.js";
import { listTemplates } from "./templates.js";
import { normalizeSpaceName } from "./model.js";
it("matches the retry fingerprints and template catalog checked by the existing Go implementation", async () => {
  const fixtures = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/onboarding-fingerprints.json", import.meta.url), "utf8")) as { name: string; apps: { ID: string }[]; fingerprint: string }[];
  for (const fixture of fixtures) expect(requestFingerprint({ name: fixture.name, apps: [...fixture.apps].sort((a, b) => a.ID < b.ID ? -1 : 1) })).toBe(fixture.fingerprint);
  expect(listTemplates()).toEqual(JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/space-templates.json", import.meta.url), "utf8")));
  expect(normalizeSpaceName("\u0085Space\u0085")).toBe("Space");
  expect(normalizeSpaceName("\ufeffSpace\ufeff")).toBe("\ufeffSpace\ufeff");
});
