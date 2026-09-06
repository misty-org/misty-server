import { afterAll, describe, expect, it } from "vitest";
import { withTransaction } from "./transaction.js";
import { createTestDatabase } from "./test-database.js";

const pool = createTestDatabase();
afterAll(() => pool.end());

describe("PostgreSQL transaction isolation", () => {
  it("does not leak an RLS identity into the next request on a reused connection", async () => {
    const client = await pool.connect();
    try {
      // Pin a physical connection to ensure the second transaction reuses it.
      const pinnedPool = { connect: async () => new Proxy(client, { get(target, key) {
        if (key === "release") return () => {};
        const value = Reflect.get(target, key);
        return typeof value === "function" ? value.bind(target) : value;
      } }) };
      const first = await withTransaction(pinnedPool, async (tx) => {
        const result = await tx.query("SELECT current_setting('app.current_user_id', true) AS user_id");
        return result.rows[0].user_id;
      }, { mode: "user", userId: "account-a" });
      expect(first).toBe("account-a");
      const second = await withTransaction(pinnedPool, async (tx) => {
        const result = await tx.query("SELECT NULLIF(current_setting('app.current_user_id', true), '') AS user_id");
        return result.rows[0].user_id;
      });
      expect(second).toBeNull();
    } finally { client.release(); }
  });
  it("rolls back database writes on operation failure", async () => {
    await expect(withTransaction(pool, async (tx) => {
      await tx.query("CREATE TABLE misty_transaction_rollback_test (id integer)");
      throw new Error("abort");
    })).rejects.toThrow("abort");
    const result = await pool.query("SELECT to_regclass('misty_transaction_rollback_test') AS relation");
    expect(result.rows[0].relation).toBeNull();
  });
});
