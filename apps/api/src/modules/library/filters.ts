import { SpaceError } from "../spaces/model.js";
import { coverOnly, itemAudience } from "./model.js";
import type { libraryQuery } from "./search.js";
const metadata = "lower(f.original_filename||' '||f.intrinsic_metadata::text||' '||COALESCE((SELECT string_agg(d.metadata::text,' ') FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata'),''))";
export const subtypes = [["selfies", "Selfies"], ["live-photos", "Live Photos"], ["portraits", "Portraits"], ["panoramas", "Panoramas"], ["slo-mo", "Slo-mo"], ["cinematic", "Cinematic"], ["bursts", "Bursts"], ["raw", "RAW"], ["screenshots", "Screenshots"], ["screen-recordings", "Screen Recordings"], ["spatial", "Spatial"]] as const;
export function mediaSubtype(kind: string) {
  const expression: Record<string, string> = { selfies: "(selfie|front.camera)", "live-photos": "(live.photo|motion.photo)", portraits: "(portrait|depth.effect)", panoramas: "(panorama|pano)", "slo-mo": "(slo.mo|slow.motion|high.frame.rate)", cinematic: "(cinematic|depth.video)", bursts: "(burst|burst.identifier)", screenshots: "(screenshot|screen.shot)", "screen-recordings": "(screen.recording|screen.capture)", spatial: "(spatial|stereo.scopic|vision.pro)" };
  const stacks: Record<string, string> = { "live-photos": "live_photo", bursts: "burst", raw: "raw_pair" };
  const mime = ["slo-mo", "cinematic", "screen-recordings"].includes(kind) ? "b.server_detected_mime_type LIKE 'video/%'" : kind === "spatial" ? "(b.server_detected_mime_type LIKE 'image/%' OR b.server_detected_mime_type LIKE 'video/%')" : "b.server_detected_mime_type LIKE 'image/%'";
  const match = kind === "raw" ? "lower(f.original_filename) ~ '\\.(dng|cr2|cr3|nef|nrw|arw|srf|sr2|raf|rw2|orf|pef|x3f)$'" : expression[kind] ? `${mime} AND ${metadata} ~ '${expression[kind]}'` : "FALSE";
  return stacks[kind] ? `(EXISTS(SELECT 1 FROM space_library_asset_stacks asset_stack WHERE asset_stack.space_id=i.space_id AND asset_stack.cover_item_id=i.id AND asset_stack.kind='${stacks[kind]}' AND asset_stack.lifecycle_state='ready') OR ${match})` : match;
}
export function listFilter(spaceId: string, userId: string, query: ReturnType<typeof libraryQuery>) {
  const args: unknown[] = [spaceId, query.state, userId], conditions = ["i.space_id=$1", "i.lifecycle_state=$2", itemAudience("i", "$3")];
  const add = (value: unknown) => { args.push(value); return `$${args.length}`; };
  if (query.state === "ready") conditions.push(coverOnly);
  if (query.visibility !== "all") conditions.push(`i.hidden=${query.visibility === "hidden" ? "TRUE" : "FALSE"}`);
  if (query.favorite) conditions.push("i.favorite=TRUE");
  if (query.structuredFavorite !== undefined) conditions.push(`i.favorite=${add(query.structuredFavorite)}`);
  for (const tag of query.tags) conditions.push(`EXISTS(SELECT 1 FROM jsonb_array_elements_text(i.tags) search_tag WHERE lower(search_tag)=lower(${add(tag)}))`);
  if (query.text) { const text = add(query.text); conditions.push(`(to_tsvector('simple',i.display_name||' '||i.caption||' '||i.tags::text) @@ plainto_tsquery('simple',${text}) OR to_tsvector('simple',f.original_filename||' '||f.intrinsic_metadata::text) @@ plainto_tsquery('simple',${text}))`); }
  if (["image", "video", "audio"].includes(query.mediaType)) conditions.push(`b.server_detected_mime_type LIKE ${add(`${query.mediaType}/%`)}`);
  else if (query.mediaType === "document") conditions.push("b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'");
  else if (subtypes.some(([kind]) => kind === query.mediaType)) conditions.push(mediaSubtype(query.mediaType));
  else if (query.mediaType) throw new SpaceError("invalid_request");
  if (query.albumId) conditions.push(`EXISTS(SELECT 1 FROM space_album_items ai JOIN space_albums a ON a.id=ai.album_id WHERE ai.space_library_item_id=i.id AND a.id=${add(query.albumId)} AND a.space_id=i.space_id)`);
  if (query.album) { const album = add(query.album); conditions.push(`EXISTS(SELECT 1 FROM space_album_items ai JOIN space_albums a ON a.id=ai.album_id WHERE ai.space_library_item_id=i.id AND a.space_id=i.space_id AND (a.id=${album} OR lower(a.name)=lower(${album})))`); }
  const captured = "COALESCE(i.date_override,f.intrinsic_capture_at,f.original_uploaded_at)";
  let sort = query.sort === "date-captured" ? captured : query.sort === "name" ? "lower(i.display_name)" : query.sort === "size" ? "b.byte_size" : "i.added_at";
  const utility: Record<string, string> = {
    "recently-viewed": "EXISTS(SELECT 1 FROM space_library_item_views v WHERE v.space_id=i.space_id AND v.space_library_item_id=i.id AND v.user_id=$3)",
    "recently-edited": "i.current_edit_version_id IS NOT NULL",
    "recently-shared": "(EXISTS(SELECT 1 FROM space_library_grants g WHERE g.source_space_id=i.space_id AND g.source_item_id=i.id AND g.state='active') OR EXISTS(SELECT 1 FROM space_message_library_references r WHERE r.space_id=i.space_id AND r.space_library_item_id=i.id))",
    "recently-saved": "EXISTS(SELECT 1 FROM space_message_attachments a WHERE a.space_id=i.space_id AND a.promoted_item_id=i.id)",
    recovered: "EXISTS(SELECT 1 FROM space_library_audit_events e WHERE e.space_id=i.space_id AND e.target_kind='library_item' AND e.target_id=i.id AND e.action='library.item.restored' AND e.outcome='success')",
    imports: "EXISTS(SELECT 1 FROM space_library_imports h WHERE h.destination_space_id=i.space_id AND h.destination_item_id=i.id AND h.state='ready')",
    featured: "b.server_detected_mime_type LIKE 'image/%' AND (i.favorite OR EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) ~ '(featured|aesthetic|best shot|high quality)'))",
    screenshots: "(lower(f.original_filename) LIKE '%screenshot%' OR lower(f.intrinsic_metadata::text) LIKE '%screenshot%')",
    documents: "b.server_detected_mime_type NOT LIKE 'image/%' AND b.server_detected_mime_type NOT LIKE 'video/%' AND b.server_detected_mime_type NOT LIKE 'audio/%'",
  };
  for (const [key, keyword] of Object.entries({ receipts: "receipt", handwriting: "handwrit", illustrations: "illustration", "qr-codes": "qr" })) utility[key] = `EXISTS(SELECT 1 FROM library_derivatives d WHERE d.space_library_item_id=i.id AND d.lifecycle_state='ready' AND d.kind='ai_metadata' AND lower(d.metadata::text) LIKE '%${keyword}%')`;
  if (query.utility) { if (!utility[query.utility]) throw new SpaceError("invalid_request"); conditions.push(utility[query.utility]!); }
  if (query.utility === "recently-viewed") sort = "(SELECT v.last_viewed_at FROM space_library_item_views v WHERE v.space_id=i.space_id AND v.space_library_item_id=i.id AND v.user_id=$3)";
  if (["recently-edited", "recovered"].includes(query.utility)) sort = "i.updated_at";
  if (query.utility === "recently-saved") sort = "i.added_at";
  if (query.dateFrom) conditions.push(`${captured}>=${add(query.dateFrom)}`);
  if (query.dateTo) conditions.push(`${captured}<${add(query.dateTo)}`);
  if (query.after) {
    const cursorSort = sort.replace(/\bi\./g, "cursor_item.").replace(/\bf\./g, "cursor_file.").replace(/\bb\./g, "cursor_blob.");
    conditions.push(`(${sort},i.id)${query.direction === "ASC" ? ">" : "<"}(SELECT ${cursorSort},cursor_item.id FROM space_library_items cursor_item JOIN library_files cursor_file ON cursor_file.id=cursor_item.file_id JOIN library_blobs cursor_blob ON cursor_blob.id=cursor_file.blob_id WHERE cursor_item.id=${add(query.after)} AND cursor_item.space_id=$1 AND ${itemAudience("cursor_item", "$3")})`);
  }
  return { args: [...args, query.limit], sql: `WHERE ${conditions.join(" AND ")} ORDER BY ${sort} ${query.direction},i.id ${query.direction} LIMIT $${args.length + 1}` };
}
