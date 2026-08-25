import { describe, expect, it } from "vitest";
import { resolveMCPEndpoint } from "../src/mcp-endpoint.js";

describe("MCP endpoint resolution", () => {
  it("preserves a public reverse-proxy path prefix", () => {
    expect(
      resolveMCPEndpoint("https://misty.example.com/api", "/mcp").toString(),
    ).toBe("https://misty.example.com/api/mcp");
  });

  it("resolves the local direct API endpoint", () => {
    expect(
      resolveMCPEndpoint("http://misty-api:8080", "/mcp").toString(),
    ).toBe("http://misty-api:8080/mcp");
  });

  it("rejects a non-rooted path from the token response", () => {
    expect(() =>
      resolveMCPEndpoint("https://misty.example.com/api", "mcp"),
    ).toThrow("invalid MCP path");
  });
});
