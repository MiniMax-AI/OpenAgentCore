import { AppError } from "./store.mjs";

// After this long without any output from a running Core Turn, the Turn is
// cancelled and its input resent once. Cancellation is impossible while an
// Environment is still provisioning, so the clock starts at the Core Turn.
const stallMs = 45_000;
const maxRetries = 1;
const statsWindowMs = 7 * 24 * 3600_000;
// Typical first-output times before Parsar has its own history, in seconds.
const typicalDefaults = { cold: 25, prepared: 6, followup: 3 };

const asInput = (input) =>
  typeof input === "string"
    ? [{ role: "user", content: [{ type: "input_text", text: input }] }]
    : input;

const median = (values) => {
  if (!values.length) return undefined;
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
};

// Plain-language failure text from Core's Session or Turn error.
export function explainFailure(error, runtimeName = "环境模板") {
  const message = typeof error === "string" ? error : error?.message || "";
  const script = message.match(/script "setup_commands\[(\d+)\]" failed with exit code (\d+)/);
  if (script)
    return `「${runtimeName}」的初始化脚本第 ${Number(script[1]) + 1} 段失败（退出码 ${script[2]}）。通常是网络波动，可以直接重试。`;
  if (/provision/i.test(message))
    return `「${runtimeName}」准备失败。通常是网络或安装问题，可以直接重试。`;
  if (/expired|no longer available/i.test(message))
    return "这个对话的工作环境已过期，请开始新对话。";
  if (/unresponsive/.test(message)) return "模型服务暂时没有响应，已自动重试仍未恢复。可以稍后重试。";
  if (/rate|429/i.test(message)) return "模型服务繁忙（请求过多），请稍后重试。";
  if (/timeout|timed out/i.test(message)) return "模型服务响应超时，可以直接重试。";
  return message ? `执行失败：${message}` : "执行失败，可以直接重试。";
}

export function turnsAPI(store, core) {
  const now = () => Date.now();
  const kindOf = (turn) => (turn.kind === "followup" ? "followup" : turn.prepared ? "prepared" : "cold");
  const runtimeName = (record) => store.get("runtimes", record.runtime_id || "")?.name;

  // Median first-output seconds for one Agent and Turn kind over the stats window.
  const typical = (agentId, kind) => {
    const since = now() - statsWindowMs;
    const values = store
      .list("turns")
      .filter((t) => t.agent_id === agentId && kindOf(t) === kind && t.output_at && t.started_at > since)
      .map((t) => (t.output_at - t.started_at) / 1000);
    return Math.round(median(values) ?? typicalDefaults[kind]);
  };

  // The newest Core Turn before an input is sent; the input's Turn is newer,
  // or is this one when the input joined a running Turn.
  const baseline = async (coreSessionId) => {
    const [latest] = (await core(`/v1/agents/sessions/${coreSessionId}/turns?order=desc&limit=1`, "GET")).data || [];
    return latest ? { base_turn_id: latest.id, base_running: !["completed", "failed", "cancelled"].includes(latest.status) } : {};
  };

  const start = (record, { id, kind, input, source, prepared = false, base = {} }) => {
    if (store.get("turns", id)) return store.get("turns", id);
    const turn = {
      id,
      session_id: record.id,
      core_session_id: record.core_session_id,
      agent_id: record.agent_id,
      source: source || record.source || "web",
      kind,
      prepared,
      input: asInput(input),
      started_at: now(),
      outcome: "running",
      stage: kind === "first" && !prepared ? "preparing" : "starting",
      retries: 0,
      ...base,
    };
    store.put("turns", turn);
    return turn;
  };

  // A follow-up message from the Web composer; Feishu records its own sends.
  const send = async (sessionId, { text, key }) => {
    const record = store.get("sessions", sessionId);
    if (!record || record.deleted_at) throw new AppError(404, "会话不存在。");
    if (!record.core_session_id) throw new AppError(409, "会话仍在创建，请稍后再发。");
    if (typeof text !== "string" || !text.trim() || text.length > 100000)
      throw new AppError(400, "消息无效或过长。");
    if (typeof key !== "string" || !key || key.length > 100) throw new AppError(400, "缺少消息标识。");
    const input = asInput(text);
    const base = await baseline(record.core_session_id);
    await core(
      `/v1/agents/sessions/${record.core_session_id}/events`,
      "POST",
      { events: [{ type: "agent.session.input.message", input }] },
      key,
    );
    return start(record, { id: `${sessionId}:${key}`, kind: "followup", input, source: "web", base });
  };

  const finish = (turn, outcome, failure) => {
    const done = { ...turn, outcome, stage: undefined, done_at: now(), pending_cancel: undefined };
    if (failure) done.failure = failure;
    if (outcome === "completed" && !done.output_at) done.output_at = done.done_at;
    store.put("turns", done);
    return done;
  };

  const supersede = (record, coreTurnId) =>
    store.put("sessions", {
      ...record,
      superseded_turns: [...new Set([...(record.superseded_turns || []), coreTurnId])],
    });

  // Replace a Session whose Environment failed before any Turn ran.
  const recreate = async (turn, record, key = `${record.id}:attempt:${turn.retries + 1}`, attempt = turn.retries + 1) => {
    const created = await core("/v1/agents/sessions", "POST", record.request, key);
    const previous = record.core_session_id;
    store.transaction(() => {
      store.put("sessions", { ...record, core_session_id: created.id, superseded_turns: [] });
      // Feishu cursors belong to the replaced Core Session.
      for (const thread of store.list("feishu_threads").filter((t) => t.session_id === record.id))
        store.put("feishu_threads", { ...thread, turn_cursor: undefined, item_cursor: undefined, artifact_cursor: undefined });
      store.put("turns", { ...turn, core_session_id: created.id, retries: attempt, stage: "preparing" });
    });
    await core(`/v1/agents/sessions/${previous}`, "DELETE").catch(() => {});
  };

  const resend = async (turn, record, coreTurnId) => {
    const attempt = turn.retries + 1;
    supersede(record, coreTurnId);
    await core(
      `/v1/agents/sessions/${record.core_session_id}/events`,
      "POST",
      { events: [{ type: "agent.session.input.message", input: turn.input }] },
      `${turn.id}:resend:${attempt}`,
    );
    store.put("turns", { ...turn, retries: attempt, stage: "retrying", pending_cancel: undefined, core_turn_id: undefined });
  };

  const check = async (turn) => {
    const record = store.get("sessions", turn.session_id);
    if (!record || record.deleted_at) return finish(turn, "cancelled");
    if (record.core_session_id !== turn.core_session_id) {
      store.put("turns", { ...turn, core_session_id: record.core_session_id });
      return;
    }
    const sid = turn.core_session_id;
    const session = await core(`/v1/agents/sessions/${sid}`, "GET");
    const superseded = new Set(record.superseded_turns || []);
    const recent = (await core(`/v1/agents/sessions/${sid}/turns?order=desc&limit=20`, "GET")).data || [];
    const at = turn.base_turn_id ? recent.findIndex((t) => t.id === turn.base_turn_id) : -1;
    const newer = (at < 0 ? recent : recent.slice(0, at)).filter((t) => !superseded.has(t.id));
    // An input sent while a Turn runs joins that Turn instead of starting one.
    const coreTurn = newer[0] || (turn.base_running ? recent[at] : undefined);
    if (!coreTurn) {
      if (session.status !== "failed") return;
      if (turn.kind === "first" && record.request && turn.retries < maxRetries) return recreate(turn, record);
      return finish(turn, "failed", explainFailure(session.error, runtimeName(record)));
    }
    const items = ((await core(`/v1/agents/sessions/${sid}/items?order=desc&limit=50`, "GET")).data || []).filter(
      (item) => item.turn_id === coreTurn.id && !(item.type === "message" && item.role === "user"),
    );
    let current = { ...turn, core_turn_id: coreTurn.id };
    if (items.length && !current.output_at) {
      current.output_at = now();
      // The frozen request is only needed until the first Turn produced output.
      if (record.request) {
        const { request, ...rest } = record;
        store.put("sessions", rest);
      }
    }
    if (coreTurn.status === "completed") return finish(current, "completed");
    if (coreTurn.status === "cancelled") {
      if (current.pending_cancel === coreTurn.id && !items.length)
        return resend(current, store.get("sessions", turn.session_id), coreTurn.id);
      return finish(current, "cancelled");
    }
    if (coreTurn.status === "failed") {
      if (!items.length && current.retries < maxRetries)
        return resend(current, store.get("sessions", turn.session_id), coreTurn.id);
      return finish(current, "failed", explainFailure(coreTurn.error || session.error, runtimeName(record)));
    }
    if (session.status === "requires_action") current.stage = "waiting_user";
    else if (current.pending_cancel) current.stage = "retrying";
    else current.stage = items.length ? "working" : "starting";
    const stalled =
      !items.length &&
      !current.pending_cancel &&
      session.status === "in_progress" &&
      now() - (coreTurn.created_at ? coreTurn.created_at * 1000 : current.started_at) > stallMs;
    if (stalled && current.retries < maxRetries) {
      await core(
        `/v1/agents/sessions/${sid}/events`,
        "POST",
        { events: [{ type: "agent.session.input.cancel" }] },
        `${turn.id}:cancel:${current.retries + 1}`,
      );
      current = { ...current, pending_cancel: coreTurn.id, stage: "retrying" };
    } else if (stalled && !current.failure) {
      current.failure = explainFailure("unresponsive");
    }
    store.put("turns", current);
  };

  let monitoring = false;
  const monitor = async () => {
    if (monitoring) return;
    monitoring = true;
    try {
      for (const turn of store.list("turns").filter((t) => t.outcome === "running"))
        await check(turn).catch(() => {
          /* Core may be briefly unreachable; the next pass retries. */
        });
    } finally {
      monitoring = false;
    }
  };

  // The latest Turn of a conversation, as the UI and Feishu cards show it.
  const summary = (sessionId) => {
    const turn = store
      .list("turns")
      .filter((t) => t.session_id === sessionId)
      .sort((a, b) => b.started_at - a.started_at)[0];
    if (!turn) return undefined;
    const kind = kindOf(turn);
    const expected_s = typical(turn.agent_id, kind);
    const elapsed_s = Math.round(((turn.done_at || now()) - turn.started_at) / 1000);
    return {
      outcome: turn.outcome,
      stage: turn.stage,
      kind,
      started_at: turn.started_at,
      elapsed_s,
      expected_s,
      slow: turn.outcome === "running" && !turn.output_at && elapsed_s > Math.max(2 * expected_s, expected_s + 15),
      retries: turn.retries,
      failure: turn.failure,
    };
  };

  // Seven-day reliability and latency for one Agent.
  const health = (agentId) => {
    const since = now() - statsWindowMs;
    const turns = store.list("turns").filter((t) => t.agent_id === agentId && t.started_at > since);
    const settled = turns.filter((t) => ["completed", "failed"].includes(t.outcome));
    const firstOutput = turns.filter((t) => t.output_at).map((t) => (t.output_at - t.started_at) / 1000);
    return {
      turns: settled.length,
      success_rate: settled.length ? settled.filter((t) => t.outcome === "completed").length / settled.length : null,
      median_first_output_s: median(firstOutput) ?? null,
      retried: turns.filter((t) => t.retries > 0).length,
    };
  };

  // Manual retry of the latest failed Turn: a failed Environment is replaced,
  // otherwise the input is sent again as a new Turn.
  const retry = async (sessionId) => {
    const record = store.get("sessions", sessionId);
    const turn = store
      .list("turns")
      .filter((t) => t.session_id === sessionId)
      .sort((a, b) => b.started_at - a.started_at)[0];
    if (!record || !turn || turn.outcome !== "failed") throw new AppError(409, "没有可以重试的失败执行。");
    const session = await core(`/v1/agents/sessions/${record.core_session_id}`, "GET");
    const id = `${turn.id}:manual:${now()}`;
    if (session.status === "failed" && record.request && !turn.core_turn_id) {
      const next = start(record, { id, kind: turn.kind, input: turn.input, source: turn.source, prepared: false });
      await recreate(next, record, id, 0);
      return summary(sessionId);
    }
    const base = await baseline(record.core_session_id);
    await core(
      `/v1/agents/sessions/${record.core_session_id}/events`,
      "POST",
      { events: [{ type: "agent.session.input.message", input: turn.input }] },
      id,
    );
    start(record, { id, kind: "followup", input: turn.input, source: turn.source, base });
    return summary(sessionId);
  };

  const superseded = (sessionId, coreTurnId) =>
    (store.get("sessions", sessionId)?.superseded_turns || []).includes(coreTurnId);
  const recovering = (sessionId) =>
    store.list("turns").some((t) => t.session_id === sessionId && t.outcome === "running");

  return { start, send, baseline, monitor, summary, health, retry, superseded, recovering, typical, explainFailure };
}
