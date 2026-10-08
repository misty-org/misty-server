import { describe, expect, it } from "vitest";

import { ClipError, MAX_CLIP_BYTES, parseClip, quotaDay, sha256Hex } from "../src/clips";

const sha = "c".repeat(64);
const clip = { clip_id: "clip_abcdefgh", revision: 3, size: 1024, blobs: [sha, sha], manifest: "AAAA" };

describe("clip rules", () => {
  it("accepts a well-formed clip and drops duplicate blobs", () => {
    expect(parseClip(clip)).toEqual({ ...clip, blobs: [sha] });
  });

  it("rejects oversized clips and malformed blob ids", () => {
    expect(() => parseClip({ ...clip, size: MAX_CLIP_BYTES + 1 })).toThrow(
      new ClipError("clip_too_large", 413),
    );
    expect(() => parseClip({ ...clip, blobs: ["not-a-hash"] })).toThrow(new ClipError("clip_malformed"));
    expect(() => parseClip({ ...clip, manifest: "" })).toThrow(ClipError);
    expect(() => parseClip(null)).toThrow(ClipError);
  });

  it("counts quotas per UTC day and hashes blobs", async () => {
    expect(quotaDay(Date.UTC(2026, 9, 7, 23, 59))).toBe("2026-10-07");
    expect(await sha256Hex(new TextEncoder().encode("abc").buffer as ArrayBuffer)).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
  });
});
