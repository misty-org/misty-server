import { afterEach, describe, expect, it, vi } from "vitest";

import type { Env } from "../src/room_env";
import { baseClaimantHolds, FILE_VERSIONS_PATH, readDriveVersions, versionClaimHolds } from "../src/room_file_versions";

const env = {
  JOURNAL_COLLAB_PROJECTION_SECRET: btoa("a projection secret of 32 bytes!"),
  MISTY_INTERNAL_API_BASE: "https://api.example.test/v1/",
} as unknown as Env;
const storage = { put: vi.fn(async () => undefined) } as unknown as DurableObjectStorage;
const target = { spaceID: "space-a", itemID: "drv_a", userID: "user-a" };

function answering(status: number, body: unknown) {
  const calls: Request[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push(new Request(url, init));
      return new Response(typeof body === "string" ? body : JSON.stringify(body), { status });
    }),
  );
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("drive versions callback", () => {
  it("asks the server, signed, for the room's item and reads its versions", async () => {
    const calls = answering(200, { item_id: "drv_a", version: 7, content_version: 5, size: 3 });
    expect(await readDriveVersions(storage, env, target)).toEqual({ version: 7, contentVersion: 5 });
    expect(calls).toHaveLength(1);
    const [call] = calls;
    expect(call?.url).toBe(`https://api.example.test/v1${FILE_VERSIONS_PATH}`);
    expect(call?.headers.get("X-Misty-Signature")).toBeTruthy();
    expect(await call?.json()).toEqual({ space_id: "space-a", item_id: "drv_a", user_id: "user-a" });
  });

  it("reads who last changed the item when the server names them", async () => {
    answering(200, { version: 7, content_version: 7, modified_by: "user-b" });
    expect(await readDriveVersions(storage, env, target)).toEqual({ version: 7, contentVersion: 7, modifiedBy: "user-b" });
    answering(200, { version: 7, content_version: 7, modified_by: "" });
    expect(await readDriveVersions(storage, env, target)).toEqual({ version: 7, contentVersion: 7 });
  });

  it("reads a server that names no content version by its version alone", async () => {
    answering(200, { version: 4 });
    expect(await readDriveVersions(storage, env, target)).toEqual({ version: 4 });
  });

  it("takes the server's refusal as an answer, and anything else as no answer", async () => {
    answering(404, { code: "not_found" });
    expect(await readDriveVersions(storage, env, target)).toBe("refused");
    answering(403, { code: "forbidden" });
    expect(await readDriveVersions(storage, env, target)).toBe("refused");
    answering(404, "404 page not found");
    expect(await readDriveVersions(storage, env, target)).toBeNull();
    answering(503, { code: "unavailable" });
    expect(await readDriveVersions(storage, env, target)).toBeNull();
    answering(401, { code: "unauthorized" });
    expect(await readDriveVersions(storage, env, target)).toBeNull();
    answering(200, { version: "7" });
    expect(await readDriveVersions(storage, env, target)).toBeNull();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("network down");
      }),
    );
    expect(await readDriveVersions(storage, env, target)).toBeNull();
  });
});

describe("version claims", () => {
  const drive = { version: 7, contentVersion: 5 };

  it("raises a base only to a version whose content is the drive's current content", () => {
    expect(versionClaimHolds("base", 5, drive, 0)).toBe(true);
    expect(versionClaimHolds("base", 7, drive, 0)).toBe(true);
    expect(versionClaimHolds("base", 4, drive, 0)).toBe(false);
    expect(versionClaimHolds("base", 8, drive, 0)).toBe(false);
    expect(versionClaimHolds("base", 7, { version: 7 }, 0)).toBe(true);
    expect(versionClaimHolds("base", 9, { version: 7 }, 0)).toBe(false);
  });

  it("takes a kept copy only for a version the drive reached with content the room never saved", () => {
    expect(versionClaimHolds("kept", 7, drive, 4)).toBe(true);
    expect(versionClaimHolds("kept", 6, drive, 4)).toBe(true);
    expect(versionClaimHolds("kept", 8, drive, 4)).toBe(false);
    expect(versionClaimHolds("kept", 7, drive, 5)).toBe(false);
    expect(versionClaimHolds("kept", 7, { version: 7 }, 6)).toBe(true);
    expect(versionClaimHolds("kept", 7, { version: 7 }, 7)).toBe(false);
    expect(versionClaimHolds("outside", 7, drive, 4)).toBe(true);
    expect(versionClaimHolds("outside", 8, drive, 4)).toBe(false);
    expect(versionClaimHolds("outside", 7, drive, 5)).toBe(false);
  });

  it("raises a base for the drive's current content only from the person who wrote it, or after a rename only from the writer", () => {
    const written = { version: 7, contentVersion: 7, modifiedBy: "user-b" };
    expect(baseClaimantHolds(written, "user-b", false)).toBe(true);
    expect(baseClaimantHolds(written, "user-b", true)).toBe(true);
    expect(baseClaimantHolds(written, "user-c", false)).toBe(false);
    expect(baseClaimantHolds(written, "user-c", true)).toBe(false);
    expect(baseClaimantHolds(written, undefined, false)).toBe(false);
    const renamed = { version: 8, contentVersion: 7, modifiedBy: "user-b" };
    expect(baseClaimantHolds(renamed, "user-b", false)).toBe(false);
    expect(baseClaimantHolds(renamed, "user-c", true)).toBe(true);
    expect(baseClaimantHolds({ version: 7, contentVersion: 7 }, "user-b", false)).toBe(false);
    expect(baseClaimantHolds({ version: 7, contentVersion: 7 }, "user-b", true)).toBe(true);
    expect(baseClaimantHolds({ version: 7, modifiedBy: "user-b" }, "user-b", false)).toBe(false);
    expect(baseClaimantHolds({ version: 7, modifiedBy: "user-b" }, "user-b", true)).toBe(true);
  });
});
