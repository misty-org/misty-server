export const PURGED_KEY = "document:purged";

export interface RoomState {
  aclVersion: number;
  purged: boolean;
}
