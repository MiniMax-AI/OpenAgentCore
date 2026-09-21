// Called inside the original native task INSERT transaction, before child work
// is admitted. All descendants share the root Session's concurrency budget.
export function enforceSubagentAdmission(db, task, configured = process.env.PARSAR_MCODE_MAX_SUBAGENTS) {
  if (task.kind !== 'subagent') return;
  const limit = Number(configured);
  if (!Number.isSafeInteger(limit) || limit < 1) throw new Error('Native Subagent admission is disabled');
  const root = db.prepare(`WITH RECURSIVE ancestors(session_id,parent_session_id) AS (
    SELECT session_id,parent_session_id FROM local_runtime_sessions WHERE session_id=?
    UNION SELECT s.session_id,s.parent_session_id FROM local_runtime_sessions s JOIN ancestors a ON s.session_id=a.parent_session_id
  ) SELECT session_id FROM ancestors WHERE parent_session_id IS NULL`).get(task.ownerSessionId);
  if (!root?.session_id) throw new Error('Native Subagent root ownership is unavailable');
  const row = db.prepare(`WITH RECURSIVE tree(session_id) AS (
    SELECT ? UNION SELECT s.session_id FROM local_runtime_sessions s JOIN tree t ON s.parent_session_id=t.session_id
  ) SELECT count(DISTINCT coalesce(json_extract(b.record_json,'$.metadata.childSessionId'),b.task_id)) AS active
    FROM local_runtime_background_tasks b JOIN tree t ON t.session_id=b.owner_session_id
    LEFT JOIN local_runtime_turn_ingress i ON i.turn_id=json_extract(b.record_json,'$.metadata.subTurnId')
    WHERE b.kind='subagent' AND (b.status IN ('queued','running','stopping') OR i.status='accepted')`).get(root.session_id);
  if (!Number.isSafeInteger(row?.active) || row.active >= limit) throw new Error('Native Subagent concurrency limit reached');
}
