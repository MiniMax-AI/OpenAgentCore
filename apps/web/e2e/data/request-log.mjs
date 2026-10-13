/** Synthetic current-process HTTP observations for the console acceptance fixture. */
export function requestLog(url) {
  const surface = url.searchParams.get("surface") ?? "public";
  const entry = (n, values) => ({ request_id: `req_${String(n).padStart(32, "0")}`, trace_id: String(n).padStart(32, "0"), started_at: `2026-10-13T10:00:0${n}Z`, method: "GET", route: "/v1/agents/{agent_id}", state: "completed", status: 200, duration_ms: 12, response_bytes: 512, error_code: null, ...values });
  const retained = surface === "public" ? [
    entry(4, { route: "/v1/agents/sessions/{session_id}/events", state: "active", status: 200, duration_ms: 2400, response_bytes: 1024 }),
    entry(3, { route: "/v1/files/{file_id}", status: 401, error_code: "invalid_api_key", response_bytes: 128 }),
    entry(2, { method: "POST", route: "/v1/agents/sessions/{session_id}/events", status: 202 }),
    entry(1, { route: "/v1/agents/sessions/{session_id}/events", state: "aborted", status: 200 }),
  ] : [entry(5, { route: "/core/v1/projects" })];
  const filtered = retained.filter((row) => (!url.searchParams.get("method") || row.method === url.searchParams.get("method")) && (!url.searchParams.get("request_id") || row.request_id === url.searchParams.get("request_id")) &&
    (url.searchParams.get("outcome") !== "active" || row.state === "active") &&
    (url.searchParams.get("outcome") !== "errors" || row.status >= 400 || row.state === "aborted"));
  const data = filtered.slice(0, Number(url.searchParams.get("limit") ?? 100));
  return { object: "core.request_log.list", instance_id: "fixture-process", instance_started_at: "2026-10-13T09:00:00Z", surface, capacity: 1000, retained_count: retained.length, total_matching: filtered.length, has_more: data.length < filtered.length, data };
}
