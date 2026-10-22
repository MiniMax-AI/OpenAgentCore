import { randomUUID } from "node:crypto";
import { prepareSession } from "./sessions.mjs";
import { AppError } from "./store.mjs";
import { explainFailure } from "./turns.mjs";

const limitMs = 5 * 60_000;
const prompt = "只回复 OK，不要调用任何工具。";

// A trial run creates one real Session for an Agent that uses the environment
// template, waits for its first reply, records the outcome and deletes the Session.
export function trialAPI(store, core) {
  const get = (runtimeId) => {
    const trial = store.get("runtime_trials", runtimeId);
    if (trial?.state === "running" && Date.now() - trial.started_at > limitMs + 30_000)
      return { ...trial, state: "failed", failure: "试运行被中断，请重新开始。" };
    return trial || null;
  };

  const settle = async (trial, sessionId, runtimeName) => {
    let result = { state: "failed", failure: "5 分钟内没有完成。" };
    try {
      while (Date.now() - trial.started_at < limitMs) {
        const session = await core(`/v1/agents/sessions/${sessionId}`, "GET");
        if (session.status === "failed") {
          result = { state: "failed", failure: explainFailure(session.error, runtimeName) };
          break;
        }
        const items = (await core(`/v1/agents/sessions/${sessionId}/items?limit=20`, "GET")).data || [];
        if (items.some((item) => item.type === "message" && item.role === "assistant")) {
          result = { state: "succeeded" };
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 1500));
      }
    } catch (error) {
      result = { state: "failed", failure: explainFailure(error.message, runtimeName) };
    }
    store.put("runtime_trials", { ...trial, ...result, seconds: Math.round((Date.now() - trial.started_at) / 1000) });
    await core(`/v1/agents/sessions/${sessionId}`, "DELETE").catch(() => {});
  };

  const start = async (runtimeId) => {
    const runtime = store.get("runtimes", runtimeId);
    if (!runtime) throw new AppError(404, "环境模板不存在。");
    const running = get(runtimeId);
    if (running?.state === "running") return running;
    const agent = store.list("agents").find((a) => a.runtime_id === runtimeId && a.enabled !== false);
    if (!agent) throw new AppError(400, "先把这个环境模板绑定到一个 Agent，再试运行。");
    const { request } = prepareSession(store, randomUUID(), { agent_id: agent.id, name: "试运行", input: prompt });
    request.metadata = { ...request.metadata, application: "parsar-trial", title: "试运行" };
    const started_at = Date.now();
    const session = await core("/v1/agents/sessions", "POST", request, randomUUID());
    const trial = { id: runtimeId, state: "running", started_at, agent_name: agent.name, runtime_revision: runtime.revision };
    store.put("runtime_trials", trial);
    void settle(trial, session.id, runtime.name);
    return trial;
  };

  return { get, start };
}
