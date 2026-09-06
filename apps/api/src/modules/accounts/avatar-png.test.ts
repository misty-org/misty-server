import { readFileSync } from "node:fs";
import { expect, it } from "vitest";
import { AvatarError, avatarMaxBytes, avatarPngConfig } from "./avatar-png.js";

it("matches Go's actual avatar PNG boundary across shared metadata fixtures", () => {
  const fixtures = JSON.parse(readFileSync(new URL("../../../../../docs/migration/fixtures/avatar-png.json", import.meta.url), "utf8")) as { name: string; base64: string; status: number }[];
  for (const fixture of fixtures) {
    let status = 200;
    try { avatarPngConfig(Buffer.from(fixture.base64, "base64")); }
    catch (error) { expect(error).toBeInstanceOf(AvatarError); status = (error as AvatarError).code === "too_large" ? 413 : 400; }
    expect(status, fixture.name).toBe(fixture.status);
  }
  expect(() => avatarPngConfig(Buffer.alloc(avatarMaxBytes + 1))).toThrow("too_large");
});
