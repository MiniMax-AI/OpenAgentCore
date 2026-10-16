import { CronExpressionParser } from "cron-parser";
import { requireSessionSettled, requireAutomaticRuntime } from "./sessions.mjs";
import { randomUUID } from "node:crypto";
import { AppError, text, uuid, transientError } from "./store.mjs";

export function nextOccurrence(schedule, time = Date.now()) {
  if (!schedule.cron) return time + schedule.interval_minutes * 60000;
  try {
    if (
      schedule.cron.trim().split(/\s+/).length !== 5 ||
      /[H?]/.test(schedule.cron)
    )
      throw new Error();
    new Intl.DateTimeFormat("en", { timeZone: schedule.timezone }).format(time);
    return CronExpressionParser.parse(schedule.cron, {
      currentDate: time,
      tz: schedule.timezone,
    })
      .next()
      .getTime();
  } catch {
    throw new AppError(400, "请填写有效的五段 Cron 表达式和 IANA 时区。");
  }
}

export function scheduleAPI(store) {
  return async (method, path, body) => {
    const match = path.match(
      /^\/app\/schedules(?:\/([a-f0-9-]{36}))?(?:\/(run|history))?$/,
    );
    if (!match || (match[1] && !uuid.test(match[1])))
      throw new AppError(404, "Not found.");
    const id = match[1],
      old = id && store.get("schedules", id);
    if (match[2]) {
      if (!old) throw new AppError(404, "计划不存在。");
      if (match[2] === "history" && method === "GET")
        return store
          .list("schedule_occurrences")
          .filter((row) => row.schedule_id === id);
      if (match[2] === "run" && method === "POST") {
        if (!uuid.test(body.request_id))
          throw new AppError(400, "执行请求标识无效。");
        const receiptId = `${id}:${body.request_id}`;
        if (store.get("schedule_requests", receiptId))
          return { accepted: true };
        if (old.pending || old.run_now)
          throw new AppError(409, "计划仍有待处理的执行。");
        const template = store.get("tasks", old.task_id);
        requireAutomaticRuntime(store, template?.runtime_id);
        store.transaction(() => {
          store.put("schedules", {
            ...old,
            run_now: true,
            next_at: Date.now(),
            revision: old.revision + 1,
          });
          store.put("schedule_requests", {
            id: receiptId,
            schedule_id: id,
            created_at: Date.now(),
          });
        });
        return { accepted: true };
      }
      throw new AppError(405, "Method not allowed.");
    }
    if (method === "GET") {
      if (id && !old) throw new AppError(404, "计划不存在。");
      const publicRow = ({ pending, ...row }) => ({
        ...row,
        pending: pending ? { task_id: pending.task_id } : undefined,
      });
      return id ? publicRow(old) : store.list("schedules").map(publicRow);
    }
    if (!id || !["PUT", "DELETE"].includes(method))
      throw new AppError(405, "Method not allowed.");
    if (old && body.revision !== old.revision)
      throw new AppError(409, "计划已更新，请重新加载。");
    if (method === "DELETE") {
      if (old?.pending && store.get("tasks", old.pending.task_id))
        throw new AppError(
          409,
          "计划仍有未确认的执行，请先处理任务中的执行记录。",
        );
      store.remove("schedules", id);
      return {};
    }
    const template = store.get(
      "tasks",
      text(body.task_id, "任务模板", 36, true),
    );
    if (!template) throw new AppError(400, "请选择用作模板的任务。");
    if (body.enabled) requireAutomaticRuntime(store, template.runtime_id);
    if (body.enabled && !template.agent_id)
      throw new AppError(400, "任务模板需要 Agent 和可自动启动的运行时。");
    const cron = body.cron ? text(body.cron, "Cron", 100, true).trim() : "";
    const timezone = text(body.timezone || "UTC", "时区", 100, true);
    if (cron) nextOccurrence({ cron, timezone });
    if (
      typeof body.enabled !== "boolean" ||
      (!cron &&
        (!Number.isInteger(body.interval_minutes) ||
          body.interval_minutes < 5 ||
          body.interval_minutes > 525600))
    )
      throw new AppError(400, "间隔必须为 5 至 525600 分钟。");
    let delivery;
    if (body.delivery) {
      const bot = store.get("feishu_bots", body.delivery.bot_id);
      if (
        !bot?.enabled ||
        !bot.routes.some(
          (route) =>
            route.chat_id === body.delivery.chat_id && route.chat_id !== "p2p",
        )
      )
        throw new AppError(400, "请选择已启用飞书机器人的允许群。");
      delivery = {
        bot_id: bot.id,
        app_id: bot.app_id,
        chat_id: body.delivery.chat_id,
      };
    }
    const value = {
      id,
      delivery,
      name: text(body.name, "计划名称", 80, true),
      task_id: template.id,
      enabled: body.enabled,
      consecutive_failures: body.enabled && !old?.enabled ? 0 : old?.consecutive_failures || 0,
      interval_minutes: cron ? undefined : body.interval_minutes,
      cron,
      timezone,
      run_now: old?.run_now,
      revision: (old?.revision || 0) + 1,
      next_at:
        old?.pending && body.enabled
          ? Date.now()
          : old &&
              old.enabled === body.enabled &&
              old.interval_minutes === body.interval_minutes &&
              old.cron === cron &&
              old.timezone === timezone
            ? old.next_at
            : nextOccurrence({ ...body, cron, timezone }),
      pending: old?.pending,
      last_task_id: old?.last_task_id,
      last_error: old?.last_error,
    };
    store.put("schedules", value);
    return value;
  };
}

// Missed intervals are coalesced into one occurrence; pending work never overlaps.
export function scheduleWorker(store, product, core) {
  let timer,
    running,
    stopping = false;
  const tick = async (time = Date.now()) => {
    // Outcomes are delivery receipts for failure accounting, not a second
    // execution history. Read authoritative Session/Turn state until terminal.
    for (const occurrence of store.list("schedule_occurrences").reverse()) {
      if (occurrence.outcome) continue;
      const record = store.get("sessions", occurrence.session_id);
      if (!record?.core_session_id || record.deleted_at) {
        if (record?.deleted_at) store.put("schedule_occurrences", { ...occurrence, outcome: "unknown" });
        continue;
      }
      try {
        const prefix = `/v1/agents/sessions/${record.core_session_id}`;
        const [session, turns] = await Promise.all([core(prefix, "GET"), core(`${prefix}/turns?order=desc&limit=1`, "GET")]);
        const turn = turns.data[0];
        if (session.status !== "failed" && !(session.status === "idle" && ["completed", "failed", "cancelled"].includes(turn?.status))) continue;
        const outcome = session.status === "failed" ? "failed" : turn.status;
        store.transaction(() => {
          store.put("schedule_occurrences", { ...occurrence, outcome, observed_at: time });
          const schedule = store.get("schedules", occurrence.schedule_id);
          if (schedule && !occurrence.manual) {
            const failures = outcome === "completed" ? 0 : (schedule.consecutive_failures || 0) + 1;
            store.put("schedules", { ...schedule, consecutive_failures: failures, ...(failures >= 5 ? { enabled: false, last_error: "连续 5 次执行失败或取消，计划已暂停。检查最近执行后可重新启用。" } : {}), revision: schedule.revision + 1 });
          }
        });
      } catch { /* An unavailable Core is not an execution failure. Retry observation. */ }
    }
    for (const schedule of store.list("schedules")) {
      if ((!schedule.enabled && !schedule.run_now) || schedule.next_at > time)
        continue;
      let occurrence = schedule.pending;
      try {
        if (!occurrence) {
          const latest =
            schedule.last_task_id &&
            store
              .list("sessions")
              .find((row) => row.task_id === schedule.last_task_id);
          if (latest) {
            try {
              await requireSessionSettled(core, latest);
            } catch (error) {
              if (!(error instanceof AppError) || error.status !== 409)
                throw error;
              const current = store.get("schedules", schedule.id);
              if (current)
                store.put("schedules", {
                  ...current,
                  next_at: nextOccurrence(current, time),
                  run_now: false,
                  last_error: "上次执行尚未结束，已跳过本次间隔。",
                  revision: current.revision + 1,
                });
              continue;
            }
          }
          const current = store.get("schedules", schedule.id);
          if (
            !current ||
            (!current.enabled && !current.run_now) ||
            current.revision !== schedule.revision
          )
            continue;
          const template = store.get("tasks", schedule.task_id);
          if (!template) throw new AppError(400, "任务模板不存在。");
          requireAutomaticRuntime(store, template.runtime_id);
          if (!store.get("agents", template.agent_id || ""))
            throw new AppError(
              400,
              "任务模板需要 Agent 和可自动启动的运行时，请编辑模板后重新启用。",
            );
          occurrence = {
            task_id: randomUUID(),
            run_id: randomUUID(),
            template: { ...template },
            delivery: schedule.delivery,
            manual: !!schedule.run_now,
            created_at: time,
          };
          store.put("schedules", { ...schedule, pending: occurrence });
        }
        let task = store.get("tasks", occurrence.task_id);
        if (!task)
          task = await product("PUT", `/app/tasks/${occurrence.task_id}`, {
            ...occurrence.template,
            revision: undefined,
            status: "backlog",
          });
        // A definite rejection leaves no Session. If the operator recovered
        // this occurrence from its task, adopt that run instead of dispatching again.
        const recovered =
          store.get("sessions", occurrence.run_id) ||
          store.list("sessions").find((row) => row.task_id === task.id);
        if (!recovered) requireAutomaticRuntime(store, task.runtime_id);
        await product(
          "PUT",
          `/app/tasks/${task.id}/runs/${recovered?.id || occurrence.run_id}`,
          { revision: task.revision },
        );
        store.put("schedule_occurrences", {
          id: occurrence.task_id,
          schedule_id: schedule.id,
          task_id: task.id,
          session_id: recovered?.id || occurrence.run_id,
          created_at: occurrence.created_at,
          delivery: occurrence.delivery,
          manual: occurrence.manual,
        });
        const current = store.get("schedules", schedule.id);
        if (current)
          store.put("schedules", {
            ...current,
            run_now: false,
            pending: undefined,
            last_task_id: task.id,
            last_error: undefined,
            next_at: nextOccurrence(current, time),
            revision: current.revision + 1,
          });
      } catch (error) {
        const current = store.get("schedules", schedule.id);
        const transient = transientError(error);
        if (current)
          store.put("schedules", {
            ...current,
            enabled: transient ? current.enabled : false,
            run_now: transient ? current.run_now : false,
            next_at: transient ? time + 15000 : current.next_at,
            last_task_id: occurrence?.task_id || current.last_task_id,
            last_error: transient
              ? "连接或执行结果暂未确认，将自动恢复原请求；不会创建另一轮执行。"
              : error instanceof AppError
                ? error.message
                : "计划执行失败，请检查最近任务。",
            revision: current.revision + 1,
          });
      }
    }
  };
  const cycle = async () => {
    if (stopping) return;
    running = tick();
    try {
      await running;
    } catch {
      console.error(
        "Parsar schedule worker could not access local state; retrying.",
      );
    } finally {
      if (!stopping) timer = setTimeout(cycle, 15000);
    }
  };
  return {
    tick,
    start() {
      void cycle();
    },
    async stop() {
      stopping = true;
      clearTimeout(timer);
      await running;
    },
  };
}
