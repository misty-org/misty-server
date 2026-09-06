import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "./transaction.js";

function fixture() {
  const client = Object.assign(new EventEmitter(), { query: vi.fn().mockResolvedValue({ rows: [] }), release: vi.fn() });
  const pool = { connect: vi.fn().mockResolvedValue(client) } as unknown as Pick<Pool, "connect">;
  return { client, pool };
}

describe("transaction ownership", () => {
  it("rolls back failed operations and returns the connection", async () => {
    const { pool, client } = fixture();
    const error = new Error("operation failed");
    await expect(withTransaction(pool, async () => { throw error; })).rejects.toBe(error);
    expect(client.query.mock.calls.map((call) => call[0])).toEqual(["BEGIN", "ROLLBACK"]);
    expect(client.release).toHaveBeenCalledWith(false);
  });
  it("discards a connection whose rollback failed and retains the original error", async () => {
    const { pool, client } = fixture();
    client.query.mockResolvedValueOnce({ rows: [] }).mockRejectedValueOnce(new Error("disconnected"));
    const error = new Error("original failure");
    await expect(withTransaction(pool, async () => { throw error; })).rejects.toBe(error);
    expect(client.release).toHaveBeenCalledWith(true);
  });
  it("does not commit after asynchronous connection loss and removes its owned listener", async () => {
    const { pool, client } = fixture(), error = new Error("connection terminated while awaiting provider");
    await expect(withTransaction(pool, async () => { client.emit("error", error); return "remote operation returned"; })).rejects.toBe(error);
    expect(client.query.mock.calls.map(call => call[0])).toEqual(["BEGIN", "ROLLBACK"]);
    expect(client.release).toHaveBeenCalledWith(true);
    expect(client.listenerCount("error")).toBe(0);
  });
  it("preserves existing RLS setting names and uses transaction-local parameterized values", async () => {
    const { pool, client } = fixture();
    await withTransaction(pool, async (_client: PoolClient) => "done", { mode: "session", sessionHash: "hash'quoted" });
    expect(client.query.mock.calls).toEqual([
      ["BEGIN"],
      ["SELECT set_config($1, $2, true)", ["app.rls_mode", "session"]],
      ["SELECT set_config($1, $2, true)", ["app.current_session_token_hash", "hash'quoted"]],
      ["COMMIT"],
    ]);
  });
});
