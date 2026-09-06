import { expect, it } from "vitest";
import { loadHandoffConfig, normalizeHandoffPath } from "./config.js";

it("limits browser redirects to account paths and secure configured origins", () => {
  for (const path of ["", " / ", "/settings/", "/settings/billing"]) expect(normalizeHandoffPath(path)).not.toBeNull();
  for (const path of ["//attacker.invalid", "https://attacker.invalid", "/settings/../admin", "/settings?next=evil", "/settings%2fbilling", "/settings\\privacy"]) expect(normalizeHandoffPath(path)).toBeNull();
  expect(loadHandoffConfig({}).websiteUrl).toBe("http://localhost:5174");
  for (const url of ["http://website.example", "https://user:password@website.example", "https://website.example/#fragment", "https://website.example/?token=secret", "https://website.example/path"]) {
    expect(() => loadHandoffConfig({ MISTY_WEBSITE_URL: url })).toThrow();
  }
});
