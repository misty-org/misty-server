export type AssetConfig = { noteMaxBytes: number; drawingMaxBytes: number; uploadTtlMs: number; downloadTtlMs: number };
export function loadAssetConfig(env: NodeJS.ProcessEnv): AssetConfig {
  const maximum = 15 * 1024 * 1024;
  function bytes(name: string) {
    const raw = env[name]?.trim(); if (!raw) return maximum;
    const value = Number(raw);
    if (!/^\d+$/.test(raw) || !Number.isSafeInteger(value) || value < 1 || value > maximum) throw new Error(`Invalid ${name}`);
    return value;
  }
  function duration(name: string, fallback: number) {
    const raw = env[name]?.trim(); if (!raw) return fallback;
    if (!/^(?:\d+(?:\.\d+)?(?:ms|s|m|h))+$/.test(raw)) throw new Error(`Invalid ${name}`);
    const scale: Record<string, number> = { ms: 1, s: 1000, m: 60000, h: 3600000 };
    const value = [...raw.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g)].reduce((sum, match) => sum + Number(match[1]) * scale[match[2]!]!, 0);
    if (!Number.isFinite(value) || value < 30000 || value > 3600000) throw new Error(`Invalid ${name}`);
    return value;
  }
  return { noteMaxBytes: bytes("MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES"), drawingMaxBytes: bytes("MISTY_DRAWING_ASSET_MAX_FILE_BYTES"),
    uploadTtlMs: duration("MISTY_R2_UPLOAD_URL_TTL", 900000), downloadTtlMs: duration("MISTY_R2_DOWNLOAD_URL_TTL", 120000) };
}
