import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
let executorConnected = false;
let agents = [],
  templates = [],
  skills = [],
  versions = new Map();
let sessions = [],
  histories = new Map(),
  turns = new Map(),
  receipts = new Map(),
  loseCreation = false;
const message = (turn, role, text) => ({
  id: randomUUID(),
  turn_id: turn.id,
  type: "message",
  status: "completed",
  role,
  phase: role === "assistant" ? "final_answer" : null,
  content: [{ type: role === "user" ? "input_text" : "output_text", text }],
});

const streams = new Map();
const emit = (session, event) => {
  const data = { event_id: randomUUID(), session_id: session.id, ...event };
  for (const stream of streams.get(session.id) || [])
    stream.write(`data: ${JSON.stringify(data)}\n\n`);
};
const now = () => Math.floor(Date.now() / 1000);
const page = (data) => ({
  object: "list",
  data,
  has_more: false,
  first_id: data[0]?.id || null,
  last_id: data.at(-1)?.id || null,
});
createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const bytes = Buffer.concat(chunks);
  const reply = (value, status = 200) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(value));
  };
  if (url.pathname === "/v1/models") {
    if (req.headers.authorization !== "Bearer provider-fixture-key")
      return reply({ error: { message: "Invalid provider key" } }, 401);
    return reply({
      data: [{ id: "kimi-k2.6" }, { id: "kimi-k2" }, { id: "kimi-latest" }],
    });
  }
  if (url.pathname === "/resume-output") {
    const session = sessions.at(-1),
      turn = turns.get(session.id).at(-1);
    const item = message(turn, "assistant", "断线前，断线后仍在生成");
    item.status = "in_progress";
    histories.get(session.id).push(item);
    emit(session, {
      type: "agent.session.turn.output_text.delta",
      turn_id: turn.id,
      item_id: item.id,
      output_index: 0,
      content_index: 0,
      delta: "断线后仍在生成",
    });
    return reply({});
  }
  if (url.pathname === "/drop-streams") {
    for (const peers of streams.values()) for (const peer of peers) peer.end();
    return reply({});
  }
  if (url.pathname === "/connect-executor") {
    executorConnected = true;
    return reply({});
  }
  if (/^\/v1\/agents\/environments\/[^/]+$/.test(url.pathname)) {
    return reply({
      id: url.pathname.split("/").at(-1),
      object: "agent.environment",
      type: "self_hosted",
      status: executorConnected ? "connected" : "pending",
      files: [],
      plugins: [],
      skills: [],
    });
  }
  if (url.pathname === "/health") return reply({ fixture: true });
  if (url.pathname === "/reset") {
    for (const peers of streams.values()) for (const peer of peers) peer.end();
    streams.clear();
    executorConnected = false;
    agents = [];
    sessions = [];
    histories = new Map();
    turns = new Map();
    receipts = new Map();
    loseCreation = false;
    templates = [];
    skills = [];
    versions = new Map();
    return reply({});
  }
  if (url.pathname === "/lose-creation") {
    loseCreation = true;
    return reply({});
  }
  if (url.pathname === "/counts")
    return reply({ agents, templates, skills, sessions });
  if (req.headers.authorization !== "Bearer fixture-project-key")
    return reply({ error: { message: "Wrong project key" } }, 401);
  let body = {};
  if (
    bytes.length &&
    req.headers["content-type"]?.startsWith("application/json")
  )
    body = JSON.parse(bytes);
  if (url.pathname === "/v1/agents" && req.method === "POST") {
    const agent = {
      id: randomUUID(),
      object: "agent",
      created_at: now(),
      updated_at: now(),
      ...body,
    };
    agents.push(agent);
    return reply(agent, 201);
  }
  const agent = agents.find((a) => url.pathname === `/v1/agents/${a.id}`);
  if (agent) {
    if (req.method === "DELETE") {
      agents = agents.filter((row) => row !== agent);
      return reply({ id: agent.id, object: "agent.deleted", deleted: true });
    }
    if (req.method === "POST") Object.assign(agent, body);
    return reply(agent);
  }
  if (
    url.pathname === "/v1/agents/environments/templates" &&
    req.method === "POST"
  ) {
    const template = { id: randomUUID(), ...body };
    templates.push(template);
    return reply(template, 201);
  }
  if (url.pathname === "/v1/skills" && req.method === "GET")
    return reply(page(skills));
  const versionMatch = url.pathname.match(/^\/v1\/skills\/([^/]+)\/versions$/);
  const skillMatch = url.pathname.match(/^\/v1\/skills\/([^/]+)$/);
  if (
    req.method === "POST" &&
    (url.pathname === "/v1/skills" || versionMatch)
  ) {
    if (req.headers["openai-beta"])
      return reply(
        { error: { message: "Skills must not use Beta header" } },
        400,
      );
    const form = await new Request("http://fixture", {
      method: "POST",
      headers: { "Content-Type": req.headers["content-type"] },
      body: bytes,
    }).formData();
    const file = form.get("files[]") || form.get("files");
    if (!file) return reply({ error: { message: "Missing Skill file" } }, 400);
    const content = await file.text();
    const name = content.match(/name: "([^"]+)"/)?.[1] || "review-skill";
    const description =
      content.match(/description: "([^"]+)"/)?.[1] || "Review code";
    let skill = skills.find((s) => s.id === versionMatch?.[1]);
    if (!skill) {
      skill = {
        id: `skill_${randomUUID().replaceAll("-", "")}`,
        object: "skill",
        created_at: now(),
        name,
        description,
        default_version: "1",
        latest_version: "1",
      };
      skills.push(skill);
      versions.set(skill.id, []);
    }
    const number = String(versions.get(skill.id).length + 1);
    const version = {
      id: `skillver_${randomUUID().replaceAll("-", "")}`,
      object: "skill.version",
      skill_id: skill.id,
      version: number,
      created_at: now(),
      name,
      description,
    };
    versions.get(skill.id).push(version);
    skill.latest_version = number;
    if (form.get("default") === "true") {
      skill.default_version = number;
      skill.name = name;
      skill.description = description;
    }
    return reply(versionMatch ? version : skill);
  }
  if (versionMatch) return reply(page(versions.get(versionMatch[1]) || []));
  const skill = skills.find((s) => s.id === skillMatch?.[1]);
  if (skill) {
    if (req.method === "DELETE") {
      skills = skills.filter((s) => s !== skill);
      return reply({ id: skill.id, object: "skill.deleted", deleted: true });
    }
    if (req.method === "POST") {
      skill.default_version = body.default_version;
    }
    return reply(skill);
  }
  if (url.pathname === "/v1/agents/sessions") {
    if (req.method === "GET") return reply(page([...sessions].reverse()));
    if (body.agent && "name" in body.agent)
      return reply(
        { error: { message: "Inline Agent cannot contain name" } },
        400,
      );
    const key = req.headers["idempotency-key"];
    if (receipts.has(key)) return reply(receipts.get(key));
    const saved = {
      id: randomUUID(),
      name: null,
      reasoning: {},
      service_tier: "auto",
      ...body.agent,
      multi_agent: { enabled: false, max_concurrent_subagents: null },
      text: { format: { type: "text" }, verbosity: "medium" },
    };
    const { object, created_at, updated_at, metadata, ...snapshot } = saved;
    const session = {
      id: randomUUID(),
      object: "agent.session",
      agent: snapshot,
      environment:
        body.environment.type === "none"
          ? { type: "none" }
          : body.environment.type === "self_hosted"
            ? {
                type: "self_hosted",
                id: randomUUID(),
                remote_url: "wss://core.example/api/v1/agent-daemon/ws",
                workspace_directory: body.environment.workspace_directory,
                capability_directories:
                  body.environment.capability_directories || [],
              }
            : {
                type: "openai_hosted",
                id: randomUUID(),
                capability_directories: [],
                network: { access: "enabled", allowed_domains: [] },
                packages: { npm: [], python: [], system: [] },
                files: [],
                plugins: [],
                skills: [],
              },
      ...(body.environment.type === "self_hosted" ? {
        x_agents_core: { installation: {
          status: "available", version: "fixture", expires_at: now() + 1800,
          commands: { posix: "bash fixture-native-bootstrap.sh", powershell: "& fixture-native-bootstrap.ps1" },
        } },
      } : {}),
      status: "idle",
      error: null,
      metadata: body.metadata,
      required_actions: [],
      vault_ids: [],
      usage: null,
      created_at: now(),
      last_active_at: now(),
    };
    const turn = {
      id: randomUUID(),
      object: "agent.session.turn",
      session_id: session.id,
      agent_id: saved.id,
      subagent_id: null,
      status: "completed",
      created_at: now(),
      started_at: now(),
      completed_at: now(),
      error: null,
      usage: null,
    };
    histories.set(session.id, [
      message(turn, "user", body.input),
      {
        id: randomUUID(),
        turn_id: turn.id,
        type: "command_execution",
        status: "completed",
        command: "npm test",
        cwd: "/workspace",
        output: "12 tests passed",
        exit_code: 0,
        duration_ms: 512,
      },
      message(
        turn,
        "assistant",
        "已检查登录流程并补充验证。\n\n## 结果\n\n- 修复了会话过期后的跳转。\n- 12 项测试通过。\n\n```ts\nconst session = await restoreSession();\n```\n\n可以继续检查移动端表现。",
      ),
    ]);
    turns.set(session.id, body.input ? [turn] : []);
    if (!body.input) histories.set(session.id, []);
    sessions.push(session);
    receipts.set(key, session);
    if (loseCreation) {
      loseCreation = false;
      return reply(
        { error: { message: "Fixture: creation response lost" } },
        502,
      );
    }
    return reply(session, 201);
  }
  const fileMatch = url.pathname.match(/^\/v1\/agents\/environments\/([^/]+)\/files$/);
  if (fileMatch) {
    if (req.method === "POST") return reply({
      object: "agent.environment.file", environment_id: fileMatch[1], path: body.path,
      size_bytes: Buffer.from(body.data, "base64").length,
    }, 201);
  }
  const artifactMatch = url.pathname.match(/^\/v1\/agents\/sessions\/([^/]+)\/artifacts(?:\/([^/]+)\/content)?$/);
  if (artifactMatch) {
    if (artifactMatch[2]) {
      res.writeHead(200, { "Content-Type": "application/octet-stream", "Content-Disposition": 'attachment; filename="report.bin"' });
      return res.end(Buffer.from([0, 255, 128, 13, 10]));
    }
    return reply(page([{ id: "aabbccdd-0000-4000-8000-000000000001", object: "agent.session.artifact",
      session_id: artifactMatch[1], environment_id: sessions.find((s) => s.id === artifactMatch[1])?.environment.id,
      turn_id: "aabbccdd-0000-4000-8000-000000000002", path: "/workspace/outputs/report.bin", size_bytes: 5, created_at: now() }]));
  }
  const match = url.pathname.match(
    /^\/v1\/agents\/sessions\/([^/]+)(?:\/(items|turns|events))?$/,
  );
  const session = sessions.find((entry) => entry.id === match?.[1]);
  if (!session) return reply({ error: { message: "Not found" } }, 404);
  if (!match[2]) return reply(session);
  if (match[2] === "items") return reply(page(histories.get(session.id)));
  if (match[2] === "turns")
    return reply(page([...turns.get(session.id)].reverse()));
  if (match[2] === "events" && req.method === "GET") {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    res.write(": connected\n\n");
    if (!streams.has(session.id)) streams.set(session.id, new Set());
    streams.get(session.id).add(res);
    res.on("close", () => streams.get(session.id)?.delete(res));
    return;
  }
  const event = body.events[0];
  const key = req.headers["idempotency-key"];
  if (receipts.has(key)) return reply({}, 202);
  receipts.set(key, true);
  if (event.type === "agent.session.input.cancel") {
    session.status = "idle";
    const turn = turns.get(session.id).at(-1);
    turn.status = "cancelled";
    turn.completed_at = now();
  } else {
    session.status = "in_progress";
    const turn = {
      ...turns.get(session.id)[0],
      id: randomUUID(),
      status: "in_progress",
      completed_at: null,
    };
    turns.get(session.id).push(turn);
    histories
      .get(session.id)
      .push(message(turn, "user", event.input[0].content[0].text));
    if (event.input[0].content[0].text === "Stream reply") {
      const item = { ...message(turn, "assistant", ""), status: "in_progress" };
      const base = { turn_id: turn.id, item_id: item.id, output_index: 0 };
      setTimeout(() => {
        emit(session, { ...base, type: "agent.session.turn.item.added", item });
        emit(session, {
          ...base,
          content_index: 0,
          type: "agent.session.turn.output_text.delta",
          delta: "流式第一段",
        });
      }, 250);
      setTimeout(() => {
        emit(session, {
          ...base,
          content_index: 0,
          type: "agent.session.turn.output_text.delta",
          delta: "，第二段完成。",
        });
        item.content[0].text = "流式第一段，第二段完成。";
        item.status = "completed";
        histories.get(session.id).push(item);
        session.status = "idle";
        turn.status = "completed";
        turn.completed_at = now();
        emit(session, { ...base, type: "agent.session.turn.item.done", item });
        emit(session, { type: "agent.session.idle", session });
      }, 3500);
      await new Promise((resolve) => setTimeout(resolve, 1500));
    }
    if (event.input[0].content[0].text === "Delayed response")
      await new Promise((resolve) => setTimeout(resolve, 5000));
  }
  return reply({}, 202);
}).listen(18181, "127.0.0.1");
