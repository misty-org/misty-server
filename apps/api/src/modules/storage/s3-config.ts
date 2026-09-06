import { isIP } from "node:net";
export type S3Config = { endpoint: string; region: string; bucket: string; accessKeyId: string; secretAccessKey: string; forcePathStyle: boolean };
export function loadS3Config(env: NodeJS.ProcessEnv): S3Config | null {
  const get = (primary: string, old: string) => (env[primary]?.trim() || env[old]?.trim() || "");
  const endpoint = get("MISTY_S3_ENDPOINT", "R2_ENDPOINT"), bucket = get("MISTY_S3_BUCKET", "R2_BUCKET");
  const accessKeyId = get("MISTY_S3_ACCESS_KEY_ID", "R2_ACCESS_KEY"), secretAccessKey = get("MISTY_S3_SECRET_ACCESS_KEY", "R2_SECRET_KEY");
  if (![endpoint, bucket, accessKeyId, secretAccessKey].some(Boolean)) return null;
  try {
    const url = new URL(endpoint), host = url.hostname.replace(/^\[|\]$/g, "");
    const local = host === "localhost" || host === "::1" || isIP(host) === 4 && host.startsWith("127.");
    if (url.username || url.password || url.search || url.hash || !["", "/"].includes(url.pathname) ||
      url.protocol !== "https:" && !(env.MISTY_DEPLOYMENT_MODE === "self_hosted" && local && url.protocol === "http:")) throw new Error();
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(bucket) || !accessKeyId || !secretAccessKey) throw new Error();
    const region = get("MISTY_S3_REGION", "R2_REGION") || "auto";
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(region)) throw new Error();
    const force = env.MISTY_S3_FORCE_PATH_STYLE?.trim();
    if (force && !/^(true|false|0|1)$/i.test(force)) throw new Error();
    return { endpoint: url.origin, bucket, region, accessKeyId, secretAccessKey, forcePathStyle: !force || /^(true|1)$/i.test(force) };
  } catch { throw new Error("Invalid object storage configuration"); }
}
