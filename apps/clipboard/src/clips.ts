/**
 * Clip rules shared by the router and the room. Everything here describes
 * ciphertext: the Worker never sees a key or a readable clip.
 */

export const MAX_CLIP_BYTES = 25 * 1024 * 1024;
export const MAX_DAILY_UPLOAD_BYTES = 200 * 1024 * 1024;
export const MAX_CLIPS = 20;
export const CLIP_LIFETIME_MS = 24 * 60 * 60 * 1000;
/** The encrypted manifest: text, small images and file names, never file bodies. */
export const MAX_MANIFEST_BYTES = 2 * 1024 * 1024;
export const MAX_BLOBS_PER_CLIP = 100;

const SHA256_PATTERN = /^[0-9a-f]{64}$/;
const CLIP_ID_PATTERN = /^[A-Za-z0-9_-]{8,64}$/;

export interface ClipInput {
  clip_id: string;
  revision: number;
  size: number;
  blobs: string[];
  /** Base64 AES-GCM ciphertext of the clip manifest. */
  manifest: string;
}

export interface ClipRecord extends ClipInput {
  device_id: string;
  created_at: number;
  expires_at: number;
}

export function isSha256(value: string): boolean {
  return SHA256_PATTERN.test(value);
}

export class ClipError extends Error {
  constructor(
    readonly code: string,
    readonly status = 400,
  ) {
    super(code);
    this.name = "ClipError";
  }
}

/** Validates a clip a device posts, before the room stores it. */
export function parseClip(value: unknown): ClipInput {
  const clip = value as Partial<ClipInput> | null;
  if (
    !clip ||
    typeof clip.clip_id !== "string" ||
    !CLIP_ID_PATTERN.test(clip.clip_id) ||
    typeof clip.revision !== "number" ||
    !Number.isSafeInteger(clip.revision) ||
    clip.revision < 0 ||
    typeof clip.size !== "number" ||
    !Number.isSafeInteger(clip.size) ||
    clip.size < 0 ||
    !Array.isArray(clip.blobs) ||
    typeof clip.manifest !== "string"
  )
    throw new ClipError("clip_malformed");
  if (clip.size > MAX_CLIP_BYTES) throw new ClipError("clip_too_large", 413);
  if (clip.blobs.length > MAX_BLOBS_PER_CLIP || !clip.blobs.every((blob) => isSha256(String(blob))))
    throw new ClipError("clip_malformed");
  if (clip.manifest.length === 0 || clip.manifest.length > (MAX_MANIFEST_BYTES * 4) / 3 + 4)
    throw new ClipError("clip_manifest_too_large", 413);
  return {
    clip_id: clip.clip_id,
    revision: clip.revision,
    size: clip.size,
    blobs: [...new Set(clip.blobs)],
    manifest: clip.manifest,
  };
}

/** The UTC day a quota counts against. */
export function quotaDay(nowMs: number): string {
  return new Date(nowMs).toISOString().slice(0, 10);
}

export async function sha256Hex(bytes: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}
