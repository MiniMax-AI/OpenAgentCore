import {
  createHash,
  randomBytes,
  randomUUID,
  timingSafeEqual,
} from "node:crypto";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { AppError, uuid, text, transientError } from "./store.mjs";
import { requireAutomaticRuntime } from "./sessions.mjs";
const digest = (token) => createHash("sha256").update(token).digest();
const publicToken = (row) =>
  row ? { created_at: row.created_at, expires_at: row.expires_at } : null;

export function agentMCPTokenAPI(store) {
  return (method, path) => {
    const match = path.match(/^\/app\/agents\/([a-f0-9-]{36})\/mcp-token$/);
    if (!match || !uuid.test(match[1]) || !store.get("agents", match[1]))
      throw new AppError(404, "Agent not found.");
    const id = match[1];
    if (method === "GET")
      return { credential: publicToken(store.get("agent_mcp_tokens", id)) };
    if (method === "DELETE") {
      store.remove("agent_mcp_tokens", id);
      return {};
    }
    if (method !== "POST") throw new AppError(405, "Method not allowed.");
    const token = randomBytes(32).toString("base64url");
    const row = {
      id,
      hash: digest(token).toString("hex"),
      created_at: Date.now(),
      expires_at: Date.now() + 90 * 86400000,
    };
    store.put("agent_mcp_tokens", row);
    return { credential: publicToken(row), token };
  };
}

export function agentMCPHandler(store, core, product) {
  return async (id, req, res, body) => {
    const stored = store.get("agent_mcp_tokens", id);
    const token = req.headers.authorization?.match(
      /^Bearer ([A-Za-z0-9_-]+)$/,
    )?.[1];
    const agent = store.get("agents", id);
    if (
      !agent ||
      agent.enabled === false ||
      !stored ||
      stored.expires_at <= Date.now() ||
      !token ||
      !timingSafeEqual(digest(token), Buffer.from(stored.hash, "hex"))
    ) {
      res.writeHead(401, {
        "Content-Type": "application/json",
        "Cache-Control": "no-store",
      });
      res.end(
        JSON.stringify({ error: "Invalid or expired Agent MCP credential." }),
      );
      return;
    }
    const reference = (record) => ({
      run_id: record.id,
      conversation_id: record.id,
      conversation_path: `/#/sessions/${record.id}`,
    });
    const output = async (record) => {
      const base = reference(record);
      if (!record.core_session_id) return { ...base, status: "queued" };
      const prefix = `/v1/agents/sessions/${record.core_session_id}`;
      const [session, turns] = await Promise.all([
        core(prefix, "GET"),
        core(`${prefix}/turns?order=desc&limit=1`, "GET"),
      ]);
      const turn = turns.data[0];
      let after,
        result = "";
      do {
        const page = await core(
          `${prefix}/items?order=asc&limit=100${after ? `&after=${encodeURIComponent(after)}` : ""}`,
          "GET",
        );
        for (const item of page.data)
          if (item.type === "message" && item.role === "assistant")
            result +=
              (item.content || [])
                .filter((part) => part.type === "output_text")
                .map((part) => part.text)
                .join("\n") + "\n";
        if (page.has_more && (!page.last_id || page.last_id === after))
          throw new Error("Invalid history cursor");
        after = page.has_more ? page.last_id : undefined;
      } while (after);
      return {
        ...base,
        status:
          session.status === "idle" && turn ? turn.status : session.status,
        result: result.trim(),
        ...(session.error ? { reason: session.error.message } : {}),
      };
    };
    const server = new Server(
      { name: `Parsar: ${agent.name}`, version: "1.0.0" },
      {
        capabilities: { tools: {} },
        instructions:
          "Call ask_agent to start a task, then get_run to retrieve its result. Answer business questions in Parsar. Execution uses bypass mode.",
      },
    );
    server.setRequestHandler(ListToolsRequestSchema, async () => ({
      tools: [
        {
          name: "ask_agent",
          description: `Ask ${agent.name} to perform a task using its configured model, runtime and capabilities.`,
          inputSchema: {
            type: "object",
            properties: {
              prompt: { type: "string", minLength: 1, maxLength: 32000 },
            },
            required: ["prompt"],
            additionalProperties: false,
          },
        },
        {
          name: "get_run",
          description:
            "Retrieve the current status and answer of a task started through this Agent MCP, recovering pending submission if needed. Call again while queued or running.",
          inputSchema: {
            type: "object",
            properties: { run_id: { type: "string", format: "uuid" } },
            required: ["run_id"],
            additionalProperties: false,
          },
          annotations: { readOnlyHint: false },
        },
      ],
    }));
    server.setRequestHandler(CallToolRequestSchema, async (request) => {
      try {
        let record;
        if (request.params.name === "ask_agent") {
          const prompt = text(
            request.params.arguments?.prompt,
            "Prompt",
            32000,
            true,
          ).trim();
          if (!prompt) throw new AppError(400, "Prompt cannot be empty.");
          if (store.get("agents", id)?.connection_requirement)
            throw new AppError(
              400,
              "此 Agent 需要个人连接，请从网页或飞书本人入口发起任务。",
            );
          requireAutomaticRuntime(store, agent.runtime_id);
          const runId = randomUUID();
          try {
            record = await product("PUT", `/app/sessions/${runId}`, {
              agent_id: id,
              runtime_id: agent.runtime_id,
              name: `MCP · ${agent.name}`.slice(0, 80),
              input: prompt,
            });
          } catch (error) {
            record = store.get("sessions", runId);
            if (!record || !transientError(error)) throw error;
          }
          store.put("agent_mcp_runs", { id: runId, agent_id: id });
        } else if (request.params.name === "get_run") {
          const runId = request.params.arguments?.run_id;
          if (
            !uuid.test(runId || "") ||
            store.get("agent_mcp_runs", runId)?.agent_id !== id
          )
            throw new AppError(404, "Run not found.");
          record = store.get("sessions", runId);
          if (!record || record.deleted_at)
            throw new AppError(404, "Run not found.");
          if (!record.core_session_id)
            record = await product("PUT", `/app/sessions/${runId}`, {});
        } else throw new AppError(404, "Unknown tool.");
        let value;
        try {
          value = await output(record);
        } catch (error) {
          if (request.params.name !== "ask_agent") throw error;
          // Dispatch is already durable. Preserve the handle even when its
          // immediate status read fails, so the client can recover via get_run.
          value = {
            ...reference(record),
            status: "unknown",
            reason:
              "Run submitted; status is temporarily unavailable. Call get_run with this run_id.",
          };
        }
        return {
          content: [{ type: "text", text: JSON.stringify(value) }],
          structuredContent: value,
        };
      } catch (error) {
        return {
          isError: true,
          content: [
            {
              type: "text",
              text:
                error instanceof AppError
                  ? error.message
                  : "Could not complete the request; inspect the run in Parsar.",
            },
          ],
        };
      }
    });
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: undefined,
      enableJsonResponse: true,
    });
    res.on("close", () => {
      void transport.close();
      void server.close();
    });
    await server.connect(transport);
    await transport.handleRequest(req, res, body);
  };
}
