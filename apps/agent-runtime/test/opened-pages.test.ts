import { describe, expect, it } from "vitest";
import { openedScopes, pageStateMessages } from "../src/opened-pages.js";

const opened = (scopeId: string, reused = false) =>
  JSON.stringify({ status: "open", scopeId, message: reused ? "Your own browser tab on this computer is already open" : "Your own browser tab is open" });

describe("pushed page state", () => {
  it("finds the tabs a turn opened, newest three, not tabs found already open", () => {
    const results = [
      { toolName: "screen_open", output: opened("s1") },
      { toolName: "screen_open", output: { type: "text", value: opened("s2") } },
      { toolName: "screen_open", output: opened("s3", true) },
      { toolName: "web_search", output: opened("s9") },
      { toolName: "screen_open", output: opened("s4") },
      { toolName: "screen_open", output: opened("s5") },
    ];
    expect(openedScopes(results, "screen_open")).toEqual(["s2", "s4", "s5"]);
  });

  it("adds the snapshot as the agent's own call and result", () => {
    const [call, result] = pageStateMessages("page-state-3-0", "browser_snapshot", "s1", { tree: "- button [ref=e1]" });
    expect(call).toMatchObject({ role: "assistant", content: [{ type: "tool-call", toolName: "browser_snapshot", input: { scopeId: "s1", maxChars: 12_000 } }] });
    expect(result).toMatchObject({ role: "tool", content: [{ type: "tool-result", toolCallId: "page-state-3-0" }] });
  });
});
