import { isRecord } from "./control_protocol";
import { postRoomCallbackAnswer } from "./room_callback";
import type { Env } from "./room_env";

export const FILE_VERSIONS_PATH = "/internal/journal/file-versions";
export const FILE_VERSIONS_BOUND_MS = 5_000;

export type VersionClaim = "base" | "kept" | "outside";

export interface DriveVersions {
  version: number;
  contentVersion?: number;
  modifiedBy?: string;
}

export interface VersionTarget {
  spaceID: string;
  itemID: string;
  userID: string;
}

function positive(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

export async function readDriveVersions(
  storage: DurableObjectStorage,
  env: Env,
  target: VersionTarget,
): Promise<DriveVersions | "refused" | null> {
  let reply: { status: number; answer: unknown };
  try {
    reply = await postRoomCallbackAnswer(
      storage,
      env,
      FILE_VERSIONS_PATH,
      { space_id: target.spaceID, item_id: target.itemID, user_id: target.userID },
      FILE_VERSIONS_BOUND_MS,
    );
  } catch {
    return null;
  }
  const said: Record<string, unknown> = isRecord(reply.answer) ? reply.answer : {};
  if (reply.status >= 200 && reply.status < 300) {
    const version = positive(said.version);
    if (version === undefined) return null;
    const contentVersion = positive(said.content_version);
    const modifiedBy = typeof said.modified_by === "string" && said.modified_by !== "" ? said.modified_by : undefined;
    return {
      version,
      ...(contentVersion === undefined ? {} : { contentVersion }),
      ...(modifiedBy === undefined ? {} : { modifiedBy }),
    };
  }
  return (reply.status === 403 || reply.status === 404) && typeof said.code === "string" ? "refused" : null;
}

export function versionClaimHolds(claim: VersionClaim, version: number, drive: DriveVersions, saved: number): boolean {
  if (version > drive.version) return false;
  if (claim === "base") return drive.contentVersion === undefined || version >= drive.contentVersion;
  return (drive.contentVersion ?? drive.version) > saved;
}

export function baseClaimantHolds(drive: DriveVersions, userID: string | undefined, fromWriter: boolean): boolean {
  if (drive.contentVersion === undefined || drive.version > drive.contentVersion || drive.modifiedBy === undefined) return fromWriter;
  return userID !== undefined && userID === drive.modifiedBy;
}
