import { randomUUID } from "node:crypto";
import { AppError, text, uuid } from "./store.mjs";
import {
  prepareSession,
  requireSessionSettled,
  requireAutomaticRuntime,
} from "./sessions.mjs";

import { taskStatuses, taskPriorities } from "../shared/work-profile.mjs";
const statuses = Object.keys(taskStatuses);
const priorities = Object.keys(taskPriorities);
const now = () => Math.floor(Date.now() / 1000);
const publicSession = ({ request, ...row }) => row;

// Product planning and decisions live here. Core remains the execution authority.
export function workAPI(store, core, sessions) {
  const dispatching = new Set();
  const requireRow = (kind, id) => {
    const row = typeof id === "string" && store.get(kind, id);
    if (!row) throw new AppError(404, "记录不存在。");
    return row;
  };
  const revision = (row, body) => {
    if (row && row.revision !== body.revision)
      throw new AppError(409, "内容已更新，请刷新后重试。");
  };
  const runs = (id) =>
    store.list("sessions").filter((row) => row.task_id === id);
  const activity = (task_id, type, message, extra = {}) => {
    store.put("activity", {
      id: randomUUID(),
      task_id,
      type,
      message,
      created_at: now(),
      ...extra,
    });
  };
  const update = (row, patch) => {
    const value = {
      ...row,
      ...patch,
      revision: row.revision + 1,
      updated_at: now(),
    };
    store.put("tasks", value);
    return value;
  };
  const requireSettled = async (id) => {
    const latest = runs(id)[0];
    if (!latest) return;
    await requireSessionSettled(core, latest);
  };
  const dispatch = async (taskId, runId, body) => {
    const existing = store.get("sessions", runId);
    if (existing) {
      if (existing.task_id !== taskId)
        throw new AppError(409, "执行标识已被使用。");
      return sessions(runId, {});
    }
    if (dispatching.has(taskId))
      throw new AppError(409, "正在分配任务，请稍后刷新。");
    dispatching.add(taskId);
    try {
      const before = requireRow("tasks", taskId);
      revision(before, body);
      if (before.status === "done")
        throw new AppError(409, "请先重新打开已完成的任务。");
      await requireSettled(taskId);
      const task = requireRow("tasks", taskId);
      revision(task, body);
      const project = task.project_id
        ? requireRow("projects", task.project_id)
        : null;
      const input = [
        `Task: ${task.name}`,
        task.description,
        body.comment ? `Task comment: ${body.comment}` : "",
        project ? `Project: ${project.name}\n${project.description}` : "",
        project?.repository ? `Repository: ${project.repository}` : "",
        "Deliver the requested work, describe verification and unresolved blockers. Save deliverable files under ./outputs when a workspace is available. Human review decides whether this task is complete.",
      ]
        .filter(Boolean)
        .join("\n\n");
      const row = prepareSession(store, runId, {
        name: task.name,
        agent_id: task.agent_id,
        runtime_id: task.runtime_id,
        input,
        images: body.images,
        files: body.files,
      });
      row.task_id = taskId;
      // This is a frozen product brief, not a duplicate of execution history.
      row.brief = input;
      row.request.metadata.task_id = taskId;
      store.transaction(() => {
        store.put("sessions", row);
        update(task, { status: "in_progress" });
        activity(taskId, "dispatch", "已分配给 Agent", { session_id: runId });
      });
      try {
        return await sessions(runId, {});
      } catch (error) {
        if (!store.get("sessions", runId)) {
          store.transaction(() => {
            activity(
              taskId,
              "dispatch_rejected",
              "执行请求被拒绝，请检查配置后重新分配。",
            );
          });
        }
        throw error;
      }
    } finally {
      dispatching.delete(taskId);
    }
  };
  return async (method, path, body) => {
    const match = path.match(
      /^\/app\/(projects|tasks)(?:\/([a-f0-9-]{36}))?(?:\/(activity|runs)(?:\/([a-f0-9-]{36}))?)?$/,
    );
    if (!match || [match[2], match[4]].some((id) => id && !uuid.test(id)))
      throw new AppError(404, "Not found.");
    const [, kind, id, action, operationId] = match;
    if (action) {
      if (kind !== "tasks" || !id) throw new AppError(404, "Not found.");
      requireRow("tasks", id);
      if (method === "GET" && !operationId)
        return action === "runs"
          ? runs(id).map(publicSession)
          : store
              .list("activity")
              .filter((row) => row.task_id === id)
              .reverse();
      if (method !== "PUT" || !operationId)
        throw new AppError(405, "Method not allowed.");
      if (action === "runs") return dispatch(id, operationId, body);
      const old = store.get("activity", operationId);
      const message = text(body.message, "评论", 10000, true).trim();
      if (old) {
        if (
          old.task_id !== id ||
          old.type !== "comment" ||
          old.message !== message ||
          !!old.notify_agent !== !!body.notify_agent
        )
          throw new AppError(409, "评论标识已被使用。");
        if (!old.notify_agent || old.delivery === "sent") return old;
      }
      let comment = old || {
        id: operationId,
        task_id: id,
        type: "comment",
        notify_agent: body.notify_agent === true,
        delivery: body.notify_agent === true ? "pending" : undefined,
        message,
        created_at: now(),
      };
      store.put("activity", comment);
      if (comment.notify_agent && comment.delivery !== "sent") {
        try {
          const task = requireRow("tasks", id);
          const agent = store.get("agents", task.agent_id);
          if (!agent || agent.enabled === false)
            throw new AppError(400, "请先分配一个已启用的 Agent。");
          let run = comment.session_id
            ? store.get("sessions", comment.session_id)
            : runs(id).find(
                (row) => !row.deleted_at && row.agent_id === task.agent_id,
              );
          if (!run) {
            requireAutomaticRuntime(store, task.runtime_id);
            // The comment UUID is also the creation key. A retry recovers the
            // frozen task+comment request instead of starting another Session.
            comment = { ...comment, session_id: operationId, initial: true };
            store.put("activity", comment);
            run = await dispatch(id, operationId, {
              revision: task.revision,
              comment: message,
            });
          } else {
            comment = { ...comment, session_id: run.id };
            store.put("activity", comment);
            if (!run.core_session_id) run = await sessions(run.id, {});
            if (!comment.initial && !run.core_session_id)
              throw new AppError(
                428,
                "任务正在等待个人连接，授权后请重试发送评论。",
              );
            if (!comment.initial)
              await core(
                `/v1/agents/sessions/${run.core_session_id}/events`,
                "POST",
                {
                  events: [
                    {
                      type: "agent.session.input.message",
                      input: [
                        {
                          type: "message",
                          role: "user",
                          content: [{ type: "input_text", text: message }],
                        },
                      ],
                    },
                  ],
                },
                operationId,
              );
          }
          comment = { ...comment, delivery: "sent", delivery_error: undefined };
          store.put("activity", comment);
        } catch (error) {
          store.put("activity", {
            ...comment,
            delivery_error:
              error instanceof AppError
                ? error.message
                : "发送尚未确认，可重试原评论。",
          });
          throw error;
        }
      }
      return comment;
    }
    if (method === "GET") return id ? requireRow(kind, id) : store.list(kind);
    if (!id || !["PUT", "DELETE"].includes(method))
      throw new AppError(405, "Method not allowed.");
    const previous = store.get(kind, id);
    revision(previous, body);
    if (method === "DELETE") {
      if (!previous) throw new AppError(404, "记录不存在。");
      if (
        kind === "projects" &&
        (store.list("tasks").some((row) => row.project_id === id) ||
          store
            .list("feishu_bots")
            .flatMap((bot) => bot.routes)
            .some((row) => row.project_id === id))
      )
        throw new AppError(409, "项目仍有任务，请先移动或删除任务。");
      if (
        kind === "tasks" &&
        (runs(id).length ||
          store
            .list("schedules")
            .some((row) => row.task_id === id || row.pending?.task_id === id))
      )
        throw new AppError(409, "此任务已有执行记录，请保留任务及其历史。");
      store.transaction(() => {
        store.remove(kind, id);
        if (kind === "tasks")
          for (const row of store.list("activity"))
            if (row.task_id === id) store.remove("activity", row.id);
      });
      return {};
    }
    const value = {
      id,
      name: text(body.name, "标题", 80, true).trim(),
      description: text(body.description, "说明", 40000),
      revision: (previous?.revision || 0) + 1,
      created_at: previous?.created_at || now(),
      updated_at: now(),
    };
    if (kind === "projects") {
      value.repository = text(body.repository || "", "仓库地址", 2000).trim();
      if (value.repository) {
        let url;
        try {
          url = new URL(value.repository);
        } catch {
          throw new AppError(400, "请填写有效的 HTTPS 仓库地址。");
        }
        if (
          url.protocol !== "https:" ||
          url.username ||
          url.password ||
          url.search ||
          url.hash
        )
          throw new AppError(400, "仓库地址必须是无凭据的 HTTPS 地址。");
      }
    } else {
      if (
        !statuses.includes(body.status) ||
        !priorities.includes(body.priority)
      )
        throw new AppError(400, "任务状态或优先级无效。");
      for (const [field, resource] of [
        ["project_id", "projects"],
        ["agent_id", "agents"],
        ["runtime_id", "runtimes"],
      ]) {
        value[field] = body[field] || "";
        if (value[field]) requireRow(resource, value[field]);
      }
      value.status = body.status;
      value.priority = body.priority;
      if (
        previous &&
        previous.status !== value.status &&
        ["review", "done"].includes(value.status)
      ) {
        await requireSettled(id);
        revision(requireRow(kind, id), body);
      }
      // Prevent stale edits from overwriting the dispatch status after an async Core read.
      revision(store.get(kind, id), body);
    }
    store.transaction(() => {
      store.put(kind, value);
      if (kind === "tasks") {
        if (!previous) activity(id, "created", "创建了任务");
        else if (previous.status !== value.status)
          activity(id, "status", "更新了任务状态", {
            from: previous.status,
            to: value.status,
          });
        else activity(id, "updated", "更新了任务说明或分配");
      }
    });
    return value;
  };
}
