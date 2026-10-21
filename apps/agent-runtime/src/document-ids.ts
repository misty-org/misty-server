// Page references belong to one document: an action carries the documentId of the page read its
// refs came from, so refs from a page that changed fail as stale instead of hitting the wrong
// control. A free model garbled those ids while copying them, splicing the document's id onto its
// tab's scopeId ("98b065b1-5ec0-4aad-8a47-…"), and every action on one site failed as stale until
// the run gave the site up (Odysseys, October 20).
//
// The run knows each tab's latest documentId from its own tool results. An action whose id is
// missing, malformed, or a garbled copy of the latest one (same first block, never issued) gets
// the real one. An id the tab really had before is left alone: refs from an old page still fail.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
/** documentId values anywhere in a result, nested JSON strings included. */
const documentIds = /documentId\\*"\s*:\s*\\*"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/gi;

type Fields = Record<string, unknown>;

const fields = (value: unknown): Fields | undefined =>
  value && typeof value === "object" && !Array.isArray(value) ? (value as Fields) : undefined;

export class DocumentIds {
  private readonly latest = new Map<string, string>();
  private readonly issued = new Set<string>();

  /** Notes the documentId a browser call's result reports for its tab. */
  note(input: unknown, output: unknown): void {
    const text = typeof output === "string" ? output : JSON.stringify(output ?? null);
    const found = [...text.matchAll(documentIds)].map((match) => match[1]!.toLowerCase());
    if (!found.length) return;
    for (const id of found) this.issued.add(id);
    const scopeId = fields(input)?.scopeId ?? /"scopeId"\s*:\s*"([^"]+)"/.exec(text)?.[1];
    if (typeof scopeId === "string" && scopeId) this.latest.set(scopeId, found.at(-1)!);
  }

  /** The call's input with its documentId repaired when it is missing or garbled. */
  repair(name: string, input: unknown): unknown {
    const value = fields(input);
    if (!name.startsWith("browser.") || !value || typeof value.scopeId !== "string") return input;
    const latest = this.latest.get(value.scopeId);
    if (!latest || !("documentId" in value || needsDocument(value))) return input;
    const given = typeof value.documentId === "string" ? value.documentId.toLowerCase() : "";
    const garbled = given !== latest && !this.issued.has(given) && given.slice(0, 8) === latest.slice(0, 8);
    return !given || !uuid.test(given) || garbled ? { ...value, documentId: latest } : input;
  }
}

/** Calls that act on refs need the document they came from. */
function needsDocument(value: Fields): boolean {
  return Array.isArray(value.steps) || Array.isArray(value.fields) || typeof value.ref === "string";
}
