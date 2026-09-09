import { describe, expect, it } from "vitest";
import {
  confirmedActionFallbackText,
  incompleteRequiredToolText,
  missingRequiredToolCalls,
  proactiveExecutionInstructions,
  unconfirmedToolResultReason,
  stopOnRepeatedOrTerminalToolFailure,
} from "../workflows/space-task-agent.js";

describe("proactive natural-language execution", () => {
  it("stops after an uncertain effect before the model can retry with another call ID", async () => {
    expect(
      await stopOnRepeatedOrTerminalToolFailure({
        steps: [
          {
            content: [
              {
                type: "tool-result",
                toolName: "mail_send",
                toolCallId: "send-one",
                output: { status: "uncertain" },
              },
            ],
          },
        ],
      } as any),
    ).toBe(true);
  });
  it("requires complete generated artifacts before a write", () => {
    expect(proactiveExecutionInstructions).toContain(
      "compose the complete final content before the write",
    );
    expect(proactiveExecutionInstructions).toContain(
      "title, format, count, length, and requested sections",
    );
    expect(proactiveExecutionInstructions).toContain(
      "Never create a placeholder, outline, partial draft, or empty shell",
    );
  });

  it("does not treat an omitted explicit write as success", () => {
    expect(
      missingRequiredToolCalls(
        ["notes.create", "messages.send"],
        ["notes.search", "notes.create"],
      ),
    ).toEqual(["messages.send"]);
    expect(incompleteRequiredToolText(["notes.create"])).toContain(
      "note creation",
    );
  });

  it("can safely confirm a write when the model omits final prose", () => {
    expect(confirmedActionFallbackText(["notes.create"])).toBe(
      "Done — I completed the requested note creation action.",
    );
  });

  it("does not treat denied or unavailable actions as confirmed", () => {
    expect(unconfirmedToolResultReason({ denied: true })).toContain(
      "not approved",
    );
    expect(unconfirmedToolResultReason({ unavailable: true })).toContain(
      "unavailable",
    );
    expect(unconfirmedToolResultReason({ id: "note-1" })).toBe("");
  });
});
