// Synthetic README scenes. Only the explicitly opted-in browser fixture uses them.
import { buildDemo } from "../e2e/data/routes.mjs";

export function buildScreenshotDemo(now, publicUrl) {
  const data = buildDemo(now, publicUrl);
  for (const session of data.sessions) {
    session.status = "idle";
    session.error = null;
    session.required_actions = [];
  }
  const pending = [
    ["Support triage", "approve_refund", "Review refund request"],
    ["Incident responder", "approve_rollback", "Approve checkout rollback"],
  ].map(([name, action, title], index) => {
    const session = data.sessions.find((entry) => entry.agent.name === name);
    session.status = "requires_action";
    session.metadata = { title };
    session.last_active_at = now - 180 - index * 240;
    const turn = data.turns.get(session.id).at(-1);
    if (turn) Object.assign(turn, { status: "waiting", completed_at: null, error: null });
    session.required_actions = [{ type: "function_call", call_id: `demo-approval-${index}`, turn_id: turn?.id, name: action, arguments: {} }];
    return session;
  });
  for (const session of data.sessions.filter((entry) => !pending.includes(entry)).slice(0, 6)) {
    session.status = "in_progress";
    session.last_active_at = now - 20;
    const turn = data.turns.get(session.id).at(-1);
    if (turn) Object.assign(turn, { status: "in_progress", completed_at: null, error: null, usage: null });
  }
  data.nodes.push(
    { ...data.nodes[0], id: "node-us-west" },
    { ...data.nodes[0], id: "node-eu-west" },
  );
  data.allocations.forEach((allocation, index) => {
    allocation.node_id = data.nodes[index % data.nodes.length].id;
  });
  data.nodes.forEach((node, index) => {
    const own = data.allocations.filter((entry) => entry.node_id === node.id);
    const running = own.filter((entry) => entry.compute_phase === "running").length;
    Object.assign(node, {
      name: ["worker-eu-01", "worker-us-02", "worker-ap-03", "worker-us-04", "worker-eu-05"][index],
      online: true, provider_ready: true, rollout: { state: "ready", ready_generation: 1 },
      active: running, running, retained: own.length - running, snapshots: own.length - running,
      reserved: 0, cleanup_pending: 0, max_active: 8, max_retained: 16,
      available_memory_bytes: 32 * 2 ** 30, available_disk_bytes: 240 * 2 ** 30,
      last_seen_at: new Date((now - 4) * 1000).toISOString(),
    });
    delete node.diagnostic;
  });
  data.sessions.sort((a, b) => b.created_at - a.created_at);
  return data;
}
