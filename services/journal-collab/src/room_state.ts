export const PURGED_KEY = "document:purged";
export const KEEP_DELETES_KEY = "document:keep_deletes";

export interface RoomState {
  aclVersion: number;
  purged: boolean;
}
