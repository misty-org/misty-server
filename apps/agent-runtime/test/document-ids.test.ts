import { describe, expect, it } from "vitest";
import { DocumentIds } from "../src/document-ids.js";

const real = "98b065b1-5ec0-4054-b100-4b09009a8709";
const older = "4a1ec30b-b0f6-46a1-8979-ecd34677fc38";
const scopeId = "agent-scope-77fa9154-ac1c-4aad-8a47-c72b00e9f1ca";

describe("DocumentIds", () => {
  it("repairs a garbled, missing or malformed documentId with the tab's latest", () => {
    const documents = new DocumentIds();
    documents.note({ scopeId }, { url: "about:blank", documentId: older });
    documents.note({ scopeId }, JSON.stringify({ documentId: real, tree: "- searchbox [ref=e18]" }));
    const steps = [{ ref: "e18", action: "fill", text: "jogger" }];
    const garbled = { scopeId, documentId: "98b065b1-5ec0-4aad-8a47-c72b00e9f1ca", steps };
    expect(documents.repair("browser.steps", garbled)).toMatchObject({ documentId: real });
    expect(documents.repair("browser.steps", { scopeId, steps })).toMatchObject({ documentId: real });
    expect(documents.repair("browser.fill_form", { scopeId, documentId: "doc-1", fields: [] })).toMatchObject({ documentId: real });
  });

  it("leaves a real earlier id alone, so refs from an old page still fail as stale", () => {
    const documents = new DocumentIds();
    documents.note({ scopeId }, { documentId: older });
    documents.note({ scopeId }, { documentId: real });
    const stale = { scopeId, documentId: older, steps: [] };
    expect(documents.repair("browser.steps", stale)).toBe(stale);
    const snapshot = { scopeId, maxChars: 12000 };
    expect(documents.repair("browser.snapshot", snapshot)).toBe(snapshot);
    expect(documents.repair("web.fetch", { url: "https://example.com" })).toEqual({ url: "https://example.com" });
  });
});
