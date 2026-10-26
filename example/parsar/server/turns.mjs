import { AppError } from "./store.mjs";

// After this long without any output from a running Core Turn, the Turn is
// cancelled and its input resent once. Cancellation is impossible while an
// Environment is still provisioning, so the clock starts at the Core Turn.
const stallMs = 45_000;
const maxRetries = 1;
// Core keeps input sent to an idle Session for at most five minutes.
const reservationMs = 5.5 * 60_000;
const statsWindowMs = 7 * 24 * 3600_000;
// Typical first-output times before Parsar has its own history, in seconds.
const typicalDefaults = { cold: 25, prepared: 6, followup: 3 };
const terminal = ["completed", "failed", "cancelled"];
const unresponsive = "模型服务暂时没有响应，已自动重试仍未恢复。可以稍后重试。";
const unadmitted = "这条消息没有被执行，请重新发送。";

const asInput = (input) =>
  typeof input === "string" ? [{ role: "user", content: [{ type: "input_text", text: input }] }] : input;

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
  if (/provision/i.test(message)) return `「${runtimeName}」准备失败。通常是网络或安装问题，可以直接重试。`;
  if (/expired|no longer available/i.test(message)) return "这个对话的工作环境已过期，请开始新对话。";
  if (/rate|429/i.test(message)) return "模型服务繁忙（请求过多），请稍后重试。";
  if (/timeout|timed out/i.test(message)) return "模型服务响应超时，可以直接重试。";
  return message ? `执行失败：${message}` : "执行失败，可以直接重试。";
}

// One ledger entry per user input, from Web and Feishu alike. The monitor
// watches running entries and recovers them only while nothing has run.
export function turnsAPI(store, core) {
  const now = () => Date.now();
  const kindOf = (turn) => (turn.kind === "followup" ? "followup" : turn.prepared ? "prepared" : "cold");
  const runtimeName = (record) => store.get("runtimes", record.runtime_id || "")?.name;
  const latest = (sessionId) =>
    store
      .list("turns")
      .filter((t) => t.session_id === sessionId)
      .sort((a, b) => b.started_at - a.started_at)[0];
  const post = (coreSessionId, event, key) =>
    core(`/v1/agents/sessions/${coreSessionId}/events`, "POST", { events: [event] }, key);
  const message = (input) => ({ type: "agent.session.input.message", input });

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
    const [newest] = (await core(`/v1/agents/sessions/${coreSessionId}/turns?order=desc&limit=1`, "GET")).data || [];
    return newest ? { base_turn_id: newest.id, base_running: !terminal.includes(newest.status) } : {};
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

  // Records and sends a follow-up input. The entry is written first so a
  // retried send (same id and key) keeps its original baseline.
  const deliver = async (record, input, { id, key, source }) => {
    const turn =
      store.get("turns", id) ||
      start(record, { id, kind: "followup", input, source, base: await baseline(record.core_session_id) });
    try {
      await post(record.core_session_id, message(turn.input), key);
    } catch (error) {
      if (error instanceof AppError && error.status >= 400 && error.status < 500) {
        store.remove("turns", id);
        throw error;
      }
      // A lost response can follow accepted input while Core resumes a Session.
      // Web follows the durable ledger; Feishu owns its own delivery retries.
      if (source !== "web") throw error;
    }
    return turn;
  };

  // The first input of a claimed prepared Session. Core answers the send only
  // once the Turn starts, so it runs in the background; the monitor replaces
  // the Session when its Turn does not start.
  const sendPrepared = (record) => {
    const id = `${record.id}:first`;
    start(record, { id, kind: "first", input: record.pending_input, prepared: true });
    void post(record.core_session_id, message(asInput(record.pending_input)), record.id).then(
      () => {
        const current = store.get("sessions", record.id);
        if (current?.core_session_id !== record.core_session_id) return;
        const { pending_input, ...sent } = current;
        store.put("sessions", sent);
      },
      () => {
        const turn = store.get("turns", id);
        if (turn?.outcome === "running" && turn.core_session_id === record.core_session_id)
          store.put("turns", { ...turn, unsent: true });
      },
    );
    return record;
  };

  // A follow-up message from the Web composer.
  const send = (sessionId, { text, key }) => {
    const record = store.get("sessions", sessionId);
    if (!record || record.deleted_at) throw new AppError(404, "会话不存在。");
    if (!record.core_session_id) throw new AppError(409, "会话仍在创建，请稍后再发。");
    if (typeof text !== "string" || !text.trim() || text.length > 100000) throw new AppError(400, "消息无效或过长。");
    if (typeof key !== "string" || !key || key.length > 100) throw new AppError(400, "缺少消息标识。");
    return deliver(record, text, { id: `${sessionId}:${key}`, key, source: "web" });
  };

  const finish = (turn, outcome, failure) => {
    const done = { ...turn, outcome, stage: undefined, done_at: now(), pending_cancel: undefined };
    if (failure) done.failure = failure;
    if (outcome === "completed" && !done.output_at) done.output_at = done.done_at;
    store.put("turns", done);
    return done;
  };

  // Replaces a Session whose first Turn never ran (its Environment failed, or a
  // claimed prepared Session did not start) with the identical frozen request.
  const recreate = async (turn, record, key, retries) => {
    const created = await core("/v1/agents/sessions", "POST", record.request, key);
    store.transaction(() => {
      const { pending_input, ...rest } = record;
      store.put("sessions", { ...rest, core_session_id: created.id, superseded_turns: [] });
      // Feishu cursors belong to the replaced Core Session.
      for (const thread of store.list("feishu_threads").filter((t) => t.session_id === record.id))
        store.put("feishu_threads", { ...thread, turn_cursor: undefined, item_cursor: undefined, artifact_cursor: undefined });
      store.put("turns", { ...turn, core_session_id: created.id, retries, prepared: false, unsent: undefined, stage: "preparing" });
    });
    await core(`/v1/agents/sessions/${record.core_session_id}`, "DELETE").catch(() => {});
  };

  // Sends the input again after a Core Turn ended without output; the ended
  // Turn is superseded so neither channel shows it.
  const resend = async (turn, coreTurnId) => {
    const record = store.get("sessions", turn.session_id);
    const retries = turn.retries + 1;
    store.put("sessions", {
      ...record,
      superseded_turns: [...new Set([...(record.superseded_turns || []), coreTurnId])],
    });
    await post(record.core_session_id, message(turn.input), `${turn.id}:resend:${retries}`);
    store.put("turns", { ...turn, retries, stage: "retrying", pending_cancel: undefined, core_turn_id: undefined });
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
    // Oldest first. This input's Turn is the first one after its baseline:
    // later inputs only start later Turns. An input sent while a Turn runs
    // joins that Turn instead of starting one.
    const recent = [...((await core(`/v1/agents/sessions/${sid}/turns?order=desc&limit=100`, "GET")).data || [])].reverse();
    const at = turn.base_turn_id ? recent.findIndex((t) => t.id === turn.base_turn_id) : -1;
    const coreTurn =
      recent.slice(at + 1).find((t) => !superseded.has(t.id)) || (turn.base_running && at >= 0 ? recent[at] : undefined);
    if (!coreTurn) {
      const replaceable = turn.kind === "first" && record.request && turn.retries < maxRetries;
      const notStarted = turn.prepared && (turn.unsent || now() - turn.started_at > stallMs);
      if (replaceable && (session.status === "failed" || notStarted))
        return recreate(turn, record, `${record.id}:attempt:${turn.retries + 1}`, turn.retries + 1);
      if (session.status === "failed") return finish(turn, "failed", explainFailure(session.error, runtimeName(record)));
      // Input sent to an existing Session that Core never admitted has expired.
      if ((turn.kind === "followup" || turn.prepared) && session.status === "idle" && now() - turn.started_at > reservationMs)
        return finish(turn, "failed", unadmitted);
      return;
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
    if (coreTurn.status === "cancelled")
      return current.pending_cancel === coreTurn.id && !items.length
        ? resend(current, coreTurn.id)
        : finish(current, "cancelled");
    if (coreTurn.status === "failed")
      return !items.length && current.retries < maxRetries
        ? resend(current, coreTurn.id)
        : finish(current, "failed", explainFailure(coreTurn.error || session.error, runtimeName(record)));
    if (session.status === "requires_action") current.stage = "waiting_user";
    else if (current.pending_cancel) current.stage = "retrying";
    else current.stage = items.length ? "working" : "starting";
    const stalled =
      !items.length &&
      !current.pending_cancel &&
      session.status === "in_progress" &&
      now() - (coreTurn.created_at ? coreTurn.created_at * 1000 : current.started_at) > stallMs;
    if (stalled && current.retries < maxRetries) {
      await post(sid, { type: "agent.session.input.cancel" }, `${turn.id}:cancel:${current.retries + 1}`);
      current = { ...current, pending_cancel: coreTurn.id, stage: "retrying" };
    } else if (stalled) current.failure = unresponsive;
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

  // What a Turn looks like to people: its stage, how long it has waited and
  // how long this Agent usually takes.
  const describe = (turn) => {
    const kind = kindOf(turn);
    const expected_s = typical(turn.agent_id, kind);
    const elapsed_s = Math.round(((turn.done_at || now()) - turn.started_at) / 1000);
    return {
      outcome: turn.outcome,
      stage: turn.stage,
      kind,
      started_at: turn.started_at,
      done_at: turn.done_at,
      elapsed_s,
      expected_s,
      first_output_s: turn.output_at ? Math.round((turn.output_at - turn.started_at) / 100) / 10 : null,
      slow: turn.outcome === "running" && !turn.output_at && elapsed_s > Math.max(2 * expected_s, expected_s + 15),
      retries: turn.retries,
      failure: turn.failure,
    };
  };

  // The latest Turn of a conversation, as the UI and Feishu cards show it.
  const summary = (sessionId) => {
    const turn = latest(sessionId);
    return turn && describe(turn);
  };

  // Running and recent Turns across all conversations, for the 运行 view.
  const overview = () => {
    const at = now();
    const visible = store.list("turns").filter((t) => {
      const record = store.get("sessions", t.session_id);
      return record && !record.deleted_at;
    });
    const latestOf = new Map();
    for (const t of visible)
      if (!latestOf.has(t.session_id) || latestOf.get(t.session_id).started_at < t.started_at) latestOf.set(t.session_id, t);
    const row = (t) => {
      const record = store.get("sessions", t.session_id);
      const prompt = (t.input || []).flatMap((m) => m.content || []).find((part) => part.type === "input_text")?.text || "";
      return {
        id: t.id,
        session_id: t.session_id,
        core_session_id: t.core_session_id,
        agent_id: t.agent_id,
        conversation: record.name,
        source: t.source,
        chat_type: record.feishu_chat_type,
        prompt: prompt.slice(0, 200),
        latest: latestOf.get(t.session_id) === t,
        ...describe(t),
      };
    };
    const running = visible.filter((t) => t.outcome === "running").sort((a, b) => a.started_at - b.started_at);
    const recent = visible
      .filter((t) => t.outcome !== "running")
      .sort((a, b) => b.done_at - a.done_at)
      .slice(0, 50);
    // Conversations running at each 10-second mark of the last five minutes.
    const concurrency = Array.from({ length: 31 }, (_, i) => {
      const mark = at - (30 - i) * 10_000;
      return visible.filter((t) => t.started_at <= mark && (t.done_at ?? Infinity) > mark).length;
    });
    return {
      running: running.map(row),
      recent: recent.map(row),
      stats: {
        running: running.length,
        waiting_user: running.filter((t) => t.stage === "waiting_user").length,
        failed_recent: visible.filter((t) => t.outcome === "failed" && t.done_at > at - 5 * 60_000).length,
        concurrency,
      },
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
    const turn = latest(sessionId);
    if (!record || turn?.outcome !== "failed") throw new AppError(409, "没有可以重试的失败执行。");
    const session = await core(`/v1/agents/sessions/${record.core_session_id}`, "GET");
    const id = `${turn.id}:manual:${now()}`;
    if (session.status === "failed" && record.request && !turn.core_turn_id)
      await recreate(start(record, { id, kind: turn.kind, input: turn.input, source: turn.source }), record, id, 0);
    else await deliver(record, turn.input, { id, key: id, source: turn.source });
  };

  const superseded = (sessionId, coreTurnId) =>
    (store.get("sessions", sessionId)?.superseded_turns || []).includes(coreTurnId);
  const recovering = (sessionId) => store.list("turns").some((t) => t.session_id === sessionId && t.outcome === "running");

  return { start, sendPrepared, deliver, send, monitor, summary, overview, health, retry, superseded, recovering };
}
