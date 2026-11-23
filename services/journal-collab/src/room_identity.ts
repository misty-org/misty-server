import * as decoding from "lib0/decoding";

const MESSAGE_AWARENESS = 1;
const IDENTITY_FIELD = "misty";

export function awarenessClaimsSomeoneElse(message: ArrayBuffer, userID: string): boolean {
  try {
    const decoder = decoding.createDecoder(new Uint8Array(message));
    if (decoding.readVarUint(decoder) !== MESSAGE_AWARENESS) return false;
    const update = decoding.createDecoder(decoding.readVarUint8Array(decoder));
    const count = decoding.readVarUint(update);
    for (let index = 0; index < count; index += 1) {
      decoding.readVarUint(update);
      decoding.readVarUint(update);
      const state: unknown = JSON.parse(decoding.readVarString(update));
      if (typeof state !== "object" || state === null) continue;
      const identity = (state as Record<string, unknown>)[IDENTITY_FIELD];
      if (identity === undefined) continue;
      if (typeof identity !== "object" || identity === null) return true;
      if ((identity as Record<string, unknown>).user_id !== userID) return true;
    }
    return false;
  } catch {
    return true;
  }
}
