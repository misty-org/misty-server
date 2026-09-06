import { createPrivateKey, type KeyObject } from "node:crypto";
import { isIP } from "node:net";

export type CollaborationConfig = { origin: string; privateKey: KeyObject; roomSalt: Buffer; controlSecret: Buffer;
  projectionSecret: Buffer; previousProjectionSecret: Buffer | null; issuer: string; audience: string };
export function loadCollaborationConfig(env: NodeJS.ProcessEnv): CollaborationConfig | null {
  const names = ["JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_ROOM_SALT", "JOURNAL_COLLAB_CONTROL_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS"];
  if (!names.some((name) => env[name]?.trim())) return null;
  try {
    const decode = (name: string) => {
      const value = env[name]?.trim() ?? "", bytes = Buffer.from(value, "base64");
      if (!value || value.length > 8192 || bytes.toString("base64") !== value) throw new Error();
      return bytes;
    };
    const secret = (name: string) => { const bytes = decode(name); if (bytes.length < 32) throw new Error(); return bytes; };
    const privateKey = createPrivateKey({ key: decode("JOURNAL_COLLAB_TICKET_PRIVATE_KEY"), format: "der", type: "pkcs8" });
    if (privateKey.asymmetricKeyType !== "ed25519") throw new Error();
    const host = env.PARTYKIT_HOST?.trim() || "misty-journal-collab.mistysys.workers.dev";
    let origin = `https://${host}`;
    if (env.MISTY_DEPLOYMENT_MODE === "self_hosted" && env.MISTY_COLLAB_PUBLIC_URL?.trim()) origin = env.MISTY_COLLAB_PUBLIC_URL.trim();
    else if (/[/:?#@\\\s]/.test(host)) throw new Error();
    const url = new URL(origin), hostname = url.hostname.replace(/^\[|\]$/g, "");
    const loopback = hostname === "localhost" || hostname === "::1" || isIP(hostname) === 4 && hostname.startsWith("127.");
    if (url.username || url.password || url.search || url.hash || !["", "/"].includes(url.pathname) ||
      url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) throw new Error();
    return { origin: url.origin, privateKey, roomSalt: secret("JOURNAL_COLLAB_ROOM_SALT"), controlSecret: secret("JOURNAL_COLLAB_CONTROL_SECRET"),
      projectionSecret: secret("JOURNAL_COLLAB_PROJECTION_SECRET"), previousProjectionSecret: env.JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS?.trim() ? secret("JOURNAL_COLLAB_PROJECTION_SECRET_PREVIOUS") : null,
      issuer: "misty-api", audience: "misty-journal-collab" };
  } catch { throw new Error("Invalid Journal collaboration configuration"); }
}
