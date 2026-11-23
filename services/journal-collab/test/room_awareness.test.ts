import * as encoding from "lib0/encoding";
import { describe, expect, it } from "vitest";
import type { Connection } from "partyserver";

import {
  awarenessClientIDs,
  awarenessIDsOf,
  awarenessOwnedBySomeoneElse,
  claimAwarenessIDs,
} from "../src/room_awareness";

function awarenessMessage(entries: [number, number, unknown][]): ArrayBuffer {
  const update = encoding.createEncoder();
  encoding.writeVarUint(update, entries.length);
  for (const [clientID, clock, state] of entries) {
    encoding.writeVarUint(update, clientID);
    encoding.writeVarUint(update, clock);
    encoding.writeVarString(update, JSON.stringify(state));
  }
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, 1);
  encoding.writeVarUint8Array(encoder, encoding.toUint8Array(update));
  return encoding.toUint8Array(encoder).slice().buffer;
}

function socket(id: string, userID: string, ids: number[] = []): Connection {
  let state: unknown = { userID, __ypsAwarenessIds: ids };
  return {
    id,
    get state() {
      return state;
    },
    setState(next: unknown) {
      state = typeof next === "function" ? (next as (previous: unknown) => unknown)(state) : next;
      return state;
    },
  } as unknown as Connection;
}

describe("awareness ownership", () => {
  it("reads the client ids an awareness message carries", () => {
    expect(awarenessClientIDs(awarenessMessage([[7, 1, { cursor: 1 }], [9, 2, null]]))).toEqual([7, 9]);
    expect(awarenessClientIDs(new Uint8Array([0, 0, 1, 0]).buffer)).toBeNull();
  });

  it("moves a re-sent state to the socket that sent it", () => {
    const old = socket("old", "ana", [7]);
    const fresh = socket("fresh", "ana");
    claimAwarenessIDs(fresh, [7], [], [old, fresh]);

    expect(awarenessIDsOf(fresh)).toEqual([7]);
    expect(awarenessIDsOf(old)).toEqual([]);
  });

  it("drops removed states from the sender", () => {
    const sender = socket("sender", "ana", [7, 8]);
    claimAwarenessIDs(sender, [], [7], [sender]);

    expect(awarenessIDsOf(sender)).toEqual([8]);
  });

  it("refuses a state another person's socket owns, but not the same person's", () => {
    const owner = socket("owner", "ana", [7]);
    const intruder = socket("intruder", "sam");
    const sameAgain = socket("again", "ana");

    expect(awarenessOwnedBySomeoneElse(intruder, [7], [owner, intruder])).toBe(true);
    expect(awarenessOwnedBySomeoneElse(sameAgain, [7], [owner, sameAgain])).toBe(false);
    expect(awarenessOwnedBySomeoneElse(intruder, [8], [owner, intruder])).toBe(false);
  });
});
