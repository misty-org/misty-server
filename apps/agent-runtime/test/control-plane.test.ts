import { afterEach, describe, expect, it } from "vitest";
import { controlPlaneURL } from "../src/control-plane.js";

const originalInternalApiBase = process.env.MISTY_INTERNAL_API_BASE;

afterEach(() => {
  if (originalInternalApiBase === undefined) {
    delete process.env.MISTY_INTERNAL_API_BASE;
  } else {
    process.env.MISTY_INTERNAL_API_BASE = originalInternalApiBase;
  }
});

describe("control plane URL", () => {
  it("accepts the private development Compose API hostname", () => {
    process.env.MISTY_INTERNAL_API_BASE = "http://misty-api:8080";
    expect(controlPlaneURL()).toBe("http://misty-api:8080");
  });

  it("still requires HTTPS for public hostnames", () => {
    process.env.MISTY_INTERNAL_API_BASE = "http://dev-api.mistysys.com";
    expect(() => controlPlaneURL()).toThrow(
      "MISTY_INTERNAL_API_BASE must use HTTPS",
    );
  });

  it("uses the callback origin from a signed Go run request", () => {
    process.env.MISTY_INTERNAL_API_BASE = "https://fallback.example.com";
    expect(controlPlaneURL("https://api.example.com/")).toBe(
      "https://api.example.com",
    );
  });

  it("rejects an insecure public callback origin", () => {
    expect(() => controlPlaneURL("http://api.example.com")).toThrow(
      "MISTY_INTERNAL_API_BASE must use HTTPS",
    );
  });
});
