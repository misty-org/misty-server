import { describe, expect, it } from "vitest";
import { parseMigration } from "./migrations.js";

describe("SQL migration parsing", () => {
  it("normalizes zero-padded versions to the database representation", () => {
    expect(parseMigration("001_example.sql", "-- +goose Up\nSELECT 1;").version).toBe("1");
  });
  it("preserves function bodies without executing the Down section", () => {
    const source = `-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION example() RETURNS text AS $$ BEGIN RETURN 'a;b'; END; $$ LANGUAGE plpgsql;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION example();`;
    const migration = parseMigration("20260101000000_example.sql", source);
    expect(migration.up).toContain("RETURN 'a;b';");
    expect(migration.up).not.toContain("DROP FUNCTION");
    expect(migration.version).toBe("20260101000000");
  });
  it("refuses directives it cannot safely honor", () => {
    expect(() => parseMigration("20260101000000_example.sql", "-- +goose NO TRANSACTION\n-- +goose Up\nSELECT 1;")).toThrow("unsupported");
  });
  it("refuses missing Up sections", () => {
    expect(() => parseMigration("20260101000000_example.sql", "DROP TABLE users;")).toThrow("no Up");
  });
});
