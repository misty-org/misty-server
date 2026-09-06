import { z } from "zod";
import { SpaceError, trimSpace } from "../spaces/model.js";
const aliases: Record<string, string[]> = { image: ["image", "images", "photo", "photos"], video: ["video", "videos"], audio: ["audio"], document: ["document", "documents", "file", "files"],
  selfies: ["selfie", "selfies"], "live-photos": ["live-photo", "live-photos"], portraits: ["portrait", "portraits"], panoramas: ["panorama", "panoramas", "pano"],
  "slo-mo": ["slo-mo", "slow-motion"], cinematic: ["cinematic"], bursts: ["burst", "bursts"], screenshots: ["screenshot", "screenshots"], "screen-recordings": ["screen-recording", "screen-recordings"], spatial: ["spatial"] };
function date(value: string, dayOnly = false) {
  if (!(dayOnly ? z.iso.date() : z.iso.datetime({ offset: true })).safeParse(value).success) throw new SpaceError("invalid_request");
  return new Date(dayOnly ? `${value}T00:00:00Z` : value);
}
export function libraryQuery(query: Record<string, string>) {
  const search = trimSpace(query.q ?? ""); if ([...search].length > 240) throw new SpaceError("invalid_request");
  const tokens: string[] = []; let current = "", quoted = false;
  for (const character of search) {
    if (character === '"') quoted = !quoted;
    else if (!quoted && /[ \t\n]/.test(character)) { if (current) tokens.push(current); current = ""; }
    else current += character;
  }
  if (current) tokens.push(current);
  const value = { after: query.after ?? "", limit: /^[+-]?\d+$/.test(query.limit ?? "") && Number(query.limit) >= 1 && Number(query.limit) <= 200 ? Number(query.limit) : 100,
    state: query.collection === "recently-deleted" ? "trash" : "ready", visibility: query.visibility === "all" || query.visibility === "hidden" ? query.visibility : "visible",
    direction: query.direction === "asc" ? "ASC" : "DESC", sort: query.sort ?? "", utility: query.utility ?? "", mediaType: query.media_type ?? "",
    albumId: query.album_id ?? "", favorite: query.favorite === "true", structuredFavorite: undefined as boolean | undefined, tags: [] as string[], album: "", text: "",
    dateFrom: query.date_from ? date(query.date_from) : undefined as Date | undefined, dateTo: query.date_to ? date(query.date_to) : undefined as Date | undefined };
  const words: string[] = [];
  for (const token of tokens) {
    const colon = token.indexOf(":"), key = trimSpace(token.slice(0, colon)).toLowerCase(), raw = trimSpace(token.slice(colon + 1)), lower = raw.toLowerCase();
    if (colon < 0 || !raw) { words.push(token); continue; }
    if (key === "tag") { if ([...raw].length > 80) throw new SpaceError("invalid_request"); value.tags.push(raw); }
    else if (key === "album") { if ([...raw].length > 120) throw new SpaceError("invalid_request"); value.album = raw; }
    else if (key === "type") { const type = Object.keys(aliases).find(type => aliases[type]!.includes(lower)); if (!type) throw new SpaceError("invalid_request"); value.mediaType = type; }
    else if (key === "favorite" || key === "hidden") {
      if (!["true", "false", "t", "f", "1", "0"].includes(lower)) throw new SpaceError("invalid_request");
      const boolean = ["true", "t", "1"].includes(lower);
      if (key === "hidden") value.visibility = boolean ? "hidden" : "visible"; else value.structuredFavorite = boolean;
    } else if (key === "after") value.dateFrom = date(raw, true);
    else if (key === "before") value.dateTo = date(raw, true);
    else if (key === "year") {
      const year = Number(raw); if (!/^[+-]?\d+$/.test(raw) || year < 1 || year > 9999) throw new SpaceError("invalid_request");
      value.dateFrom = date(`${String(year).padStart(4, "0")}-01-01`, true);
      value.dateTo = new Date(value.dateFrom); value.dateTo.setUTCFullYear(year + 1);
    } else words.push(token);
  }
  value.text = words.join(" "); return value;
}
