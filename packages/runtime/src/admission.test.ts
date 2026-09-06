import { expect, it } from "vitest";
import { createAdmission } from "./admission.js";

it("bounds live work, shares nested dispatch and releases failed or completed permits", async () => {
  const admission = createAdmission(1); let finish!: () => void;
  const blocked = new Promise<void>((resolve) => { finish = resolve; });
  const pending = admission.run(async () => {
    expect(await admission.run(async () => "nested", () => "rejected")).toBe("nested");
    await blocked; return "complete";
  }, () => "rejected");
  await Promise.resolve();
  expect(await admission.run(() => { throw new Error("Must not enter excess work"); }, () => "rejected")).toBe("rejected");
  finish(); expect(await pending).toBe("complete");
  await expect(admission.run(() => { throw new Error("failed"); }, () => "rejected")).rejects.toThrow("failed");
  expect(await admission.run(() => "next", () => "rejected")).toBe("next");
});

it("does not let detached descendants reuse an expired request permit", async () => {
  const admission = createAdmission(1); let runLater!: () => Promise<string>;
  await admission.run(() => {
    const resource = Promise.resolve();
    let release!: () => void; const resumed = new Promise<void>((resolve) => { release = resolve; });
    const detached = resource.then(async () => { await resumed; return admission.run(() => "bypassed", () => "rejected"); });
    runLater = async () => { release(); return detached; };
  }, () => {});
  await admission.run(async () => { expect(await runLater()).toBe("rejected"); }, () => { throw new Error("Unexpected rejection"); });
});
