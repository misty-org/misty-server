import * as Y from "yjs";

export function emptyDocumentBytes(): number {
  const document = new Y.Doc();
  try {
    return Y.encodeStateAsUpdate(document).byteLength;
  } finally {
    document.destroy();
  }
}

export function replaceSharedText(text: Y.Text, value: string): void {
  text.delete(0, text.length);
  if (value) text.insert(0, value);
}

export function markdownToPlainText(markdown: string): string {
  return markdown
    .replace(/!\[[^\]]*\]\([^)]*\)/gu, " ")
    .replace(/\[([^\]]+)\]\([^)]*\)/gu, "$1")
    .replace(/[`*_>#~-]+/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();
}

/** Writes a note's title and markdown in one transaction. */
export function writeMarkdownContent(
  document: Y.Doc,
  title: string,
  markdown: string,
  pendingVersion: number,
): void {
  document.transact(() => {
    const metadata = document.getMap<unknown>("misty:document");
    metadata.set("schema", "tiptap-v1");
    metadata.set("pending_version", pendingVersion);
    metadata.set("pending_markdown", markdown);
    replaceSharedText(document.getText("misty:title"), title);
    replaceSharedText(document.getText("misty:markdown"), markdown);
  });
}
