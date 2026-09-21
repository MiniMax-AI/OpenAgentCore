import { DatabaseSync } from 'node:sqlite';
import { realpathSync, statSync } from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

// This entry point is invoked only by the daemon, outside the workspace sandbox.
// The schema is pinned by source.json; no native code or model is executed here.
export function readSubagentSnapshot(dataDir, rootSessionId) {
  if (!isAbsolute(dataDir) || resolve(dataDir) !== dataDir || realpathSync(dataDir) !== dataDir || !rootSessionId)
    throw new Error('Invalid private native Session binding');
  const path = join(dataDir, 'v2/sqlite/runtime-state.sqlite');
  if (realpathSync(path) !== path || !statSync(path).isFile()) throw new Error('Invalid native history path');
  const db = new DatabaseSync(path, { readOnly: true });
  try {
    db.exec('PRAGMA query_only=ON; PRAGMA busy_timeout=5000; BEGIN');
    const sessions = db.prepare(`WITH RECURSIVE tree(session_id) AS (
      SELECT session_id FROM local_runtime_sessions WHERE session_id=?
      UNION SELECT s.session_id FROM local_runtime_sessions s JOIN tree t ON s.parent_session_id=t.session_id
    ) SELECT s.session_id AS id,s.parent_session_id AS parent,s.created_at_ms AS createdAt,
      s.agent_name AS name,s.session_kind AS kind,s.purpose,s.status,s.archived
      FROM local_runtime_sessions s JOIN tree t ON s.session_id=t.session_id ORDER BY s.created_at_ms,s.session_id LIMIT 10001`).all(rootSessionId);
    if (!sessions.some(s => s.id === rootSessionId)) throw new Error('Native root Session not found');
    if (sessions.length > 10000) throw new Error('Native Session snapshot exceeds complete-read bound');
    const turns = db.prepare(`SELECT turn_id AS id,status,accepted_at_ms AS createdAt,completed_at_ms AS completedAt
      FROM local_runtime_turn_ingress WHERE session_id=? ORDER BY accepted_at_ms,turn_id LIMIT 100001`);
    const messages = db.prepare(`SELECT msg_id AS id,turn_id AS turnId,role,created_at_ms AS createdAt,data_json AS data
      FROM local_runtime_message_rows WHERE session_id=? ORDER BY created_at_ms,id LIMIT 100001`);
    const tasks = db.prepare(`SELECT record_json AS data FROM local_runtime_background_tasks
      WHERE owner_session_id=? AND kind='subagent' ORDER BY created_at_ms,task_id LIMIT 100001`);
    for (const session of sessions) {
      session.turns = turns.all(session.id);
      session.messages = messages.all(session.id).map(row => ({ ...row, data: JSON.parse(row.data) }));
      session.tasks = tasks.all(session.id).map(row => JSON.parse(row.data));
      if (session.turns.length > 100000 || session.messages.length > 100000 || session.tasks.length > 100000)
        throw new Error('Native history snapshot exceeds complete-read bound');
    }
    db.exec('COMMIT');
    return { version: 1, rootSessionId, complete: true, sessions };
  } finally { db.close(); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.stdout.write(JSON.stringify(readSubagentSnapshot(process.argv[2], process.argv[3])) + '\n');
  } catch {
    // Native history may contain private model input; never echo it in diagnostics.
    process.stderr.write('MiniMax child history snapshot failed\n');
    process.exitCode = 1;
  }
}
