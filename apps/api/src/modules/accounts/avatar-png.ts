import { crc32 } from "node:zlib";

export const avatarMaxBytes = 5 * 1024 * 1024;
export class AvatarError extends Error {
  constructor(readonly code: "invalid_png" | "too_large" | "not_found" | "upload_expired" | "upload_limit") { super(code); this.name = "AvatarError"; }
}
const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
/** Match Go's png.DecodeConfig boundary, including indexed color metadata.
 * No pixel decompression or dimension-dependent allocation occurs on the server. */
export function avatarPngConfig(data: Buffer) {
  if (!data.length || data.length > avatarMaxBytes) throw new AvatarError("too_large");
  const invalid = () => { throw new AvatarError("invalid_png"); };
  if (!data.subarray(0, 8).equals(signature)) invalid();
  let offset = 8, header: { width: number; height: number; depth: number; color: number } | undefined, palette = 0;
  while (offset + 8 <= data.length) {
    const length = data.readUInt32BE(offset), type = data.toString("latin1", offset + 4, offset + 8), start = offset + 8, end = start + length;
    if (type === "IDAT") { if (!header || header.color === 3 && !palette) invalid(); return header!; }
    if (end + 4 > data.length || crc32(data.subarray(offset + 4, end)) !== data.readUInt32BE(end)) invalid();
    if (type === "IHDR") {
      if (header || length !== 13) invalid();
      const width = data.readUInt32BE(start), height = data.readUInt32BE(start + 4), depth = data[start + 8]!, color = data[start + 9]!;
      const depths: Record<number, number[]> = { 0: [1, 2, 4, 8, 16], 2: [8, 16], 3: [1, 2, 4, 8], 4: [8, 16], 6: [8, 16] };
      if (width < 1 || height < 1 || width > 4096 || height > 4096 || !depths[color]?.includes(depth) || data[start + 10] !== 0 || data[start + 11] !== 0 || data[start + 12]! > 1) invalid();
      header = { width, height, depth, color };
      if (color !== 3) return header;
    } else if (type === "PLTE") {
      if (!header || palette || length % 3 || !length || length / 3 > Math.min(256, 2 ** header.depth)) invalid();
      palette = length / 3;
    } else if (type === "tRNS") {
      if (!header || !palette || length > 256) invalid();
      return header!;
    } else if (type === "IEND") invalid();
    offset = end + 4;
  }
  return invalid();
}
