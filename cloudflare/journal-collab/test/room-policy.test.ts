import { describe, expect, it } from "vitest";

import {
  claimTicketID,
  messageIsTooLarge,
  roomIsFull,
  socketBelongsToUser,
  socketIsReadOnly,
  socketIsSuperseded,
} from "../src/room-policy";

function state(
  role: "creator" | "editor" | "viewer",
  aclVersion = 1,
  userID = `user-${role}`,
) {
  return {
    userID,
    role,
    aclVersion,
    resourceID: "resource",
    spaceID: "space",
  };
}

describe("document room policy", () => {
  it("denies viewer, unknown, and superseded sockets from writing", () => {
    expect(socketIsReadOnly(state("viewer"), 1)).toBe(true);
    expect(socketIsReadOnly(undefined, 1)).toBe(true);
    expect(socketIsReadOnly(state("editor", 1), 2)).toBe(true);
    expect(socketIsReadOnly(state("editor", 2), 2)).toBe(false);
  });

  it("claims a ticket id exactly once", async () => {
    const values = new Map<string, unknown>();
    const storage = {
      async get<T>(key: string) {
        return values.get(key) as T | undefined;
      },
      async put(key: string, value: unknown) {
        values.set(key, value);
      },
    };

    await expect(claimTicketID(storage, "ticket-1", 100)).resolves.toBe(true);
    await expect(claimTicketID(storage, "ticket-1", 101)).resolves.toBe(false);
  });

  it("identifies sockets superseded by an ACL version change", () => {
    expect(socketIsSuperseded(state("editor", 1), 2)).toBe(true);
    expect(socketIsSuperseded(state("viewer", 2), 2)).toBe(false);
    expect(socketIsSuperseded(null, 2)).toBe(true);
  });

  it("targets only revoked users, or everyone for a full disconnect", () => {
    const revoked = new Set(["user-editor"]);
    expect(socketBelongsToUser(state("editor"), revoked)).toBe(true);
    expect(
      socketBelongsToUser(state("editor", 1, "retained-user"), revoked),
    ).toBe(false);
    expect(socketBelongsToUser(undefined, revoked)).toBe(false);
    expect(socketBelongsToUser(undefined, null)).toBe(true);
  });

  it("enforces room capacity and oversized message ceilings", () => {
    expect(roomIsFull(39)).toBe(false);
    expect(roomIsFull(40)).toBe(true);
    expect(messageIsTooLarge(512 * 1024)).toBe(false);
    expect(messageIsTooLarge(512 * 1024 + 1)).toBe(true);
  });
});
