import { DatabaseSync } from "node:sqlite";
import { mkdirSync, chmodSync } from "node:fs";
import { dirname, isAbsolute, join } from "node:path";
import { homedir } from "node:os";
import { createHash } from "node:crypto";

export class AppError extends Error {
  constructor(status, message, code) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
// Public API conflicts can be transient admission contention or terminal failures.
export function transientError(error) {
  if (!(error instanceof AppError)) return true;
  if (
    [
      "environment_unavailable",
      "environment_input_expired",
      "environment_input_cancelled",
      "idempotency_conflict",
    ].includes(error.code)
  )
    return false;
  return error.status >= 500 || [408, 409, 425, 429].includes(error.status);
}
export function dataPath(config, env = process.env) {
  const directory =
    env.OAC_EXAMPLE_DATA_DIR ||
    join(homedir(), ".oac", "data", "parsar-example");
  if (!isAbsolute(directory))
    throw new Error("OAC_EXAMPLE_DATA_DIR must be absolute.");
  const scope = createHash("sha256")
    .update(config.target + "\n" + config.key)
    .digest("hex")
    .slice(0, 24);
  return join(directory, `${scope}.sqlite`);
}
export function openStore(path) {
  if (path !== ":memory:")
    mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  const db = new DatabaseSync(path);
  if (path !== ":memory:") chmodSync(path, 0o600);
  db.exec(`PRAGMA busy_timeout=5000;
    CREATE TABLE IF NOT EXISTS records (kind TEXT NOT NULL, id TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(kind,id));`);
  return {
    close: () => db.close(),
    transaction: (write) => {
      db.exec("BEGIN IMMEDIATE");
      try {
        const result = write();
        db.exec("COMMIT");
        return result;
      } catch (error) {
        db.exec("ROLLBACK");
        throw error;
      }
    },
    list: (kind) =>
      db
        .prepare("SELECT value FROM records WHERE kind=? ORDER BY rowid DESC")
        .all(kind)
        .map((r) => JSON.parse(r.value)),
    get: (kind, id) => {
      const row = db
        .prepare("SELECT value FROM records WHERE kind=? AND id=?")
        .get(kind, id);
      return row ? JSON.parse(row.value) : null;
    },
    put: (kind, value) =>
      db
        .prepare(
          "INSERT INTO records(kind,id,value) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET value=excluded.value",
        )
        .run(kind, value.id, JSON.stringify(value)),
    remove: (kind, id) =>
      db.prepare("DELETE FROM records WHERE kind=? AND id=?").run(kind, id),
  };
}
export const uuid =
  /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
export function text(value, label, max, required = false) {
  if (
    typeof value !== "string" ||
    value.length > max ||
    value.includes("\0") ||
    (required && !value.trim())
  )
    throw new AppError(400, `${label}无效或过长。`);
  return value;
}
