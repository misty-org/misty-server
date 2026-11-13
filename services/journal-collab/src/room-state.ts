/** Storage key holding the purge tombstone. */
export const PURGED_KEY = "document:purged";

/** Room facts restored from storage on start and updated by commands. */
export interface RoomState {
  /** Newest ACL version this room has been told about. */
  aclVersion: number;
  /** Prevents a deleted room from being repopulated by an already-minted ticket. */
  purged: boolean;
}
