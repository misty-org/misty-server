import { expect, it } from "vitest";
import { rpcHeaders } from "./forwarding.js";

it("forwards the separate upload credential only to Journal finalize methods", () => {
  const original = new Request("http://api.invalid/app-runtime/rpc", { headers: {
    Authorization: "Bearer app-credential", Cookie: "account=private", "X-Misty-Library-Upload-Token": "upload-credential",
    "X-Misty-Internal-Secret": "server-only", "X-Arbitrary": "untrusted",
  } });
  for (const method of ["notes.assets.finalize", "drawings.assets.finalize"]) {
    expect(Object.fromEntries(rpcHeaders(method, original)!)).toEqual({ authorization: "Bearer app-credential",
      "content-type": "application/json", "x-misty-library-upload-token": "upload-credential" });
  }
  for (const method of ["notes.create", "notes.assets.reserve", "drawings.assets.download", "billing.checkout"]) {
    expect(Object.fromEntries(rpcHeaders(method, original)!)).toEqual({ authorization: "Bearer app-credential", "content-type": "application/json" });
  }
  for (const token of ["", " ", "duplicate, second", "x".repeat(1025), "two words"]) {
    expect(rpcHeaders("notes.assets.finalize", new Request(original, { headers: { "X-Misty-Library-Upload-Token": token } }))).toBeNull();
  }
  expect(rpcHeaders("notes.assets.finalize", new Request(original.url))?.has("X-Misty-Library-Upload-Token")).toBe(false);
});
