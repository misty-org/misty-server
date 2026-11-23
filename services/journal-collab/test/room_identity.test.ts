import * as encoding from "lib0/encoding";
import { describe, expect, it } from "vitest";

import { awarenessClaimsSomeoneElse } from "../src/room_identity";

function awarenessMessage(state: Record<string, unknown> | null): ArrayBuffer {
  const update = encoding.createEncoder();
  encoding.writeVarUint(update, 1);
  encoding.writeVarUint(update, 42);
  encoding.writeVarUint(update, 1);
  encoding.writeVarString(update, JSON.stringify(state));
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, 1);
  encoding.writeVarUint8Array(encoder, encoding.toUint8Array(update));
  return encoding.toUint8Array(encoder).slice().buffer;
}

describe("awarenessClaimsSomeoneElse", () => {
  it("lets a person's own identity and states without one through", () => {
    expect(awarenessClaimsSomeoneElse(awarenessMessage({ misty: { user_id: "ana" } }), "ana")).toBe(false);
    expect(awarenessClaimsSomeoneElse(awarenessMessage({ cursor: 3 }), "ana")).toBe(false);
    expect(awarenessClaimsSomeoneElse(awarenessMessage(null), "ana")).toBe(false);
  });

  it("refuses a state that names someone else", () => {
    expect(awarenessClaimsSomeoneElse(awarenessMessage({ misty: { user_id: "sam" } }), "ana")).toBe(true);
    expect(awarenessClaimsSomeoneElse(awarenessMessage({ misty: "sam" }), "ana")).toBe(true);
  });

  it("leaves sync messages alone", () => {
    expect(awarenessClaimsSomeoneElse(new Uint8Array([0, 0, 1, 0]).buffer, "ana")).toBe(false);
  });
});
