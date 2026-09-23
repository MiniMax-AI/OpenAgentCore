import { execFileSync } from "node:child_process";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentCoreError, type SavedAgent } from "@agents-core-web/agents-client";
import { createExampleRequest, exampleForm, exampleInput, findExampleAgent, HOME_EXAMPLE_MARKER, loadExampleIdentity, saveExampleIdentity, terminalExample, type ExampleClient } from "./example-request";

const marker = "9eac6707-937e-48fc-bcac-1b2b5d2dba37";
const input = exampleInput(exampleForm("model/example", marker))!;
const agent = (id: string, example = marker): SavedAgent => ({
  id, object: "agent", name: "My first Agent", model: "model/example", instructions: null,
  metadata: { [HOME_EXAMPLE_MARKER]: example }, tools: [], reasoning: {}, service_tier: "auto",
  text: { format: { type: "text" }, verbosity: "medium" },
  multi_agent: { enabled: false, max_concurrent_subagents: null }, created_at: 1, updated_at: 1,
});
const page = (data: SavedAgent[]) => ({ object: "list" as const, data, has_more: false, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function client(): ExampleClient {
  return { listAgents: vi.fn(async () => page([])), createAgent: vi.fn(async () => agent("created")) };
}

describe("first-run request ownership", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("associates only the exact example marker, never an arbitrary or same-name Agent", () => {
    expect(findExampleAgent([agent("unrelated", "other")], marker)).toBeNull();
    expect(findExampleAgent([agent("unrelated", "other"), agent("mine")], marker)?.id).toBe("mine");
    expect(() => findExampleAgent([agent("one"), agent("two")], marker)).toThrow("more than one");
  });

  it("blocks duplicate clicks even while the preflight read is pending", async () => {
    const api = client();
    const pending = deferred<ReturnType<typeof page>>();
    vi.mocked(api.listAgents).mockReturnValueOnce(pending.promise);
    const submitted = vi.fn();
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), submitted);
    const first = attempt.run(input);
    await attempt.run(input);
    expect(api.createAgent).not.toHaveBeenCalled();
    pending.resolve(page([]));
    await first;
    await attempt.run(input);
    expect(api.createAgent).toHaveBeenCalledTimes(1);
    expect(submitted).toHaveBeenCalledTimes(1);
    expect(attempt.getState().agent?.id).toBe("created");
  });

  it("reconciles an uncertain write without issuing another create", async () => {
    const api = client();
    vi.mocked(api.createAgent).mockRejectedValueOnce(new Error("connection lost"));
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), vi.fn());
    await attempt.run(input);
    expect(attempt.getState().phase).toBe("waiting");
    await attempt.run(input);
    vi.mocked(api.listAgents).mockResolvedValueOnce(page([agent("confirmed")]));
    await attempt.reconcile();
    expect(api.createAgent).toHaveBeenCalledTimes(1);
    expect(attempt.getState().agent?.id).toBe("confirmed");
  });

  it.each([400, 401, 403, 404, 422])("allows correction and explicit retry after a definitive %s rejection, including reload", async (status) => {
    const stored = new Map<string, string>();
    vi.stubGlobal("sessionStorage", { getItem: (key: string) => stored.get(key) ?? null, setItem: (key: string, value: string) => stored.set(key, value) });
    const api = client();
    vi.mocked(api.createAgent).mockRejectedValueOnce(new AgentCoreError("private-model-key-must-not-render", status));
    const identity = { marker, submitted: false };
    const changed = vi.fn();
    const attempt = createExampleRequest(api, identity, changed, (submitted) => {
      identity.submitted = submitted; saveExampleIdentity("rejected", identity);
    });
    await attempt.run(input);
    expect(attempt.getState()).toEqual({ phase: "idle", agent: null, error: "rejected" });
    expect(identity.submitted).toBe(false);
    expect(JSON.stringify(changed.mock.calls)).not.toContain("private-model-key-must-not-render");
    await attempt.reconcile();
    expect(attempt.getState().error).toBe("rejected");
    expect(api.createAgent).toHaveBeenCalledTimes(1);
    attempt.dispose();
    const restoredIdentity = loadExampleIdentity("rejected");
    expect(restoredIdentity).toEqual({ marker, submitted: false });
    const restored = createExampleRequest(api, restoredIdentity, vi.fn(), vi.fn());
    const corrected = { ...input, model: "corrected-model" };
    await restored.run(corrected);
    expect(api.createAgent).toHaveBeenCalledTimes(2);
    expect(api.createAgent).toHaveBeenLastCalledWith(corrected);
    expect(restored.getState().phase).toBe("complete");
  });

  it.each([429, 500, 502])("keeps %s uncertain and disables retries after reload", async (status) => {
    const api = client();
    vi.mocked(api.createAgent).mockRejectedValueOnce(new AgentCoreError("unknown result", status));
    const identity = { marker, submitted: false };
    const submitted = vi.fn((value: boolean) => { identity.submitted = value; });
    const attempt = createExampleRequest(api, identity, vi.fn(), submitted);
    await attempt.run(input);
    expect(attempt.getState()).toEqual({ phase: "waiting", agent: null, error: "uncertain" });
    expect(submitted.mock.calls).toEqual([[true]]);
    await attempt.reconcile();
    expect(attempt.getState().error).toBe("uncertain");
    const restored = createExampleRequest(api, identity, vi.fn(), vi.fn());
    await restored.run(input);
    expect(api.createAgent).toHaveBeenCalledTimes(1);
  });

  it("treats malformed successful responses as uncertain rather than permitting another write", async () => {
    const api = client();
    vi.mocked(api.createAgent).mockResolvedValueOnce(agent("unrelated", "different-marker"));
    const submitted = vi.fn();
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), submitted);
    await attempt.run(input);
    expect(attempt.getState().error).toBe("uncertain");
    await attempt.run(input);
    expect(api.createAgent).toHaveBeenCalledTimes(1);
    expect(submitted.mock.calls).toEqual([[true]]);
  });

  it("does not reset persisted submission on a late rejection after disposal", async () => {
    const api = client();
    const pending = deferred<void>();
    vi.mocked(api.createAgent).mockImplementationOnce(async () => { await pending.promise; throw new AgentCoreError("rejected", 400); });
    const submitted = vi.fn();
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), submitted);
    const operation = attempt.run(input);
    await vi.waitFor(() => expect(api.createAgent).toHaveBeenCalledOnce());
    attempt.dispose(); pending.resolve(); await operation;
    expect(submitted.mock.calls).toEqual([[true]]);
  });

  it("does not duplicate an externally copied request, including a restored attempt", async () => {
    const api = client();
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), vi.fn());
    attempt.observeExternal();
    await attempt.run(input);
    const restored = createExampleRequest(api, { marker, submitted: true }, vi.fn(), vi.fn());
    await restored.run(input);
    expect(api.createAgent).not.toHaveBeenCalled();
    vi.mocked(api.listAgents).mockResolvedValueOnce(page([agent("external")]));
    await restored.reconcile();
    expect(restored.getState().agent?.id).toBe("external");
  });

  it("ignores late create completion after leaving or changing connections", async () => {
    const api = client();
    const pending = deferred<SavedAgent>();
    vi.mocked(api.createAgent).mockReturnValueOnce(pending.promise);
    const changed = vi.fn();
    const attempt = createExampleRequest(api, { marker, submitted: false }, changed, vi.fn());
    const operation = attempt.run(input);
    await vi.waitFor(() => expect(api.createAgent).toHaveBeenCalledOnce());
    attempt.dispose(); changed.mockClear();
    pending.resolve(agent("late"));
    await operation;
    expect(changed).not.toHaveBeenCalled();
  });

  it("aborts a stale preflight without creating an Agent", async () => {
    const api = client();
    const pending = deferred<ReturnType<typeof page>>();
    vi.mocked(api.listAgents).mockReturnValueOnce(pending.promise);
    const attempt = createExampleRequest(api, { marker, submitted: false }, vi.fn(), vi.fn());
    const operation = attempt.run(input);
    attempt.dispose();
    pending.resolve(page([]));
    await operation;
    expect(api.createAgent).not.toHaveBeenCalled();
  });

  it("finds an external request beyond the first collection page", async () => {
    const api = client();
    vi.mocked(api.listAgents)
      .mockResolvedValueOnce({ ...page([agent("other", "other")]), has_more: true })
      .mockResolvedValueOnce(page([agent("external")]));
    const attempt = createExampleRequest(api, { marker, submitted: true }, vi.fn(), vi.fn());
    await attempt.reconcile();
    expect(attempt.getState().agent?.id).toBe("external");
    expect(api.listAgents).toHaveBeenLastCalledWith(expect.objectContaining({ after: "other" }));
  });
});

describe("executable local request", () => {
  it("uses hidden key prompts and never puts a typed model key into code", () => {
    const code = terminalExample({ ...input, x_agents_core: { harness: "codex", model_provider: { protocol: "responses", base_url: "https://models.example/v1", api_key: "private-model-canary" } } }, "https://console.example/v1")!;
    expect(code).not.toContain("private-model-canary");
    expect(code).toContain('getpass.getpass("Core API key: ")');
    expect(code).toContain('getpass.getpass("Model API key: ")');
    expect(code).toContain('os.environ.get("CORE_API_KEY")');
    expect(code).toContain('os.environ.get("MODEL_API_KEY")');
    expect(code).toContain('"https://console.example/v1/agents"');
    expect(code).not.toContain("Basic ");
  });

  it("keeps adversarial names and instructions as exact Python string data", () => {
    const hostile = { ...input, name: "Agent ' \" \\ `$(touch nope)` 🦊", instructions: "first\nPY\n__import__('os').system('false')\n\u0000\u2028last" };
    const code = terminalExample(hostile, "https://console.example/v1")!;
    const source = code.split("\n").slice(1, -1).join("\n");
    const restored = JSON.parse(execFileSync("python3", ["-c", "import ast,json,sys; tree=ast.parse(sys.stdin.read()); assignment=next(node for node in tree.body if isinstance(node,ast.Assign) and isinstance(node.targets[0],ast.Name) and node.targets[0].id=='payload'); print(json.dumps(ast.literal_eval(assignment.value)))"], { input: source, encoding: "utf8" }));
    expect(restored).toEqual(hostile);
    expect(code.split("\n").filter((line) => line === "PY")).toHaveLength(1);
  });

  it("rejects credential-bearing API endpoints and preserves inherited defaults", () => {
    expect(terminalExample(input, "https://user:key@core.example/v1")).toBeNull();
    expect(terminalExample(input, "https://core.example/v1?key=secret")).toBeNull();
    expect(input.x_agents_core).toBeUndefined();
    expect(input.metadata?.[HOME_EXAMPLE_MARKER]).toBe(marker);
  });

  it("executes one HTTP request with separate caller and model keys", () => {
    const code = terminalExample({ ...input, x_agents_core: { harness: "codex", model_provider: { protocol: "responses", base_url: "https://models.example/v1", api_key: "must-not-be-exported" } } }, "https://console.example/v1")!;
    const source = code.split("\n").slice(1, -1).join("\n");
    const fixture = [
      "import io, json, urllib.request",
      "calls = []",
      "def test_urlopen(request):",
      "    calls.append(request)",
      '    assert request.full_url == "https://console.example/v1/agents"',
      '    assert request.get_header("Authorization") == "Bearer fixture-caller-key"',
      '    assert request.get_header("Openai-beta") == "agents=v1"',
      "    payload = json.loads(request.data)",
      '    assert payload["x_agents_core"]["model_provider"]["api_key"] == "fixture-model-key"',
      `    assert payload["metadata"]["core_home_example"] == "${marker}"`,
      '    return io.StringIO(\'{"id":"agent-from-fixture"}\')',
      "class TestOpener:",
      "    open = staticmethod(test_urlopen)",
      "urllib.request.build_opener = lambda handler: TestOpener()",
    ].join("\n");
    const output = execFileSync("python3", ["-c", `${fixture}\n${source}\nassert len(calls) == 1`], {
      env: { ...process.env, CORE_API_KEY: "fixture-caller-key", MODEL_API_KEY: "fixture-model-key" }, encoding: "utf8",
    });
    expect(output.trim()).toBe("Agent created: agent-from-fixture");
    expect(output).not.toContain("fixture-caller-key");
    expect(output).not.toContain("fixture-model-key");
  });

  it.each([301, 302, 303, 307, 308])("rejects HTTP %s redirects before credentials reach a second origin", (status) => {
    const code = terminalExample({ ...input, x_agents_core: { model_provider: { protocol: "responses", base_url: "https://models.example/v1", api_key: "not-exported" } } }, "http://127.0.0.1:1/v1")!;
    const source = code.split("\n").slice(1, -1).join("\n");
    const fixture = [
      "import contextlib, io, json, sys, threading",
      "from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer",
      "from urllib.error import HTTPError",
      "target_hits = []",
      "origin_hits = []",
      "class Target(BaseHTTPRequestHandler):",
      "    def receive(self):",
      '        target_hits.append({"method": self.command, "authorization": self.headers.get("Authorization")})',
      "        self.send_response(200)",
      "        self.end_headers()",
      '        self.wfile.write(b\'{"id":"redirected-agent"}\')',
      "    do_GET = receive",
      "    do_POST = receive",
      "    def log_message(self, *args): pass",
      'target = ThreadingHTTPServer(("127.0.0.1", 0), Target)',
      "class Origin(BaseHTTPRequestHandler):",
      "    def do_POST(self):",
      '        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))',
      '        origin_hits.append({"caller_key": self.headers.get("Authorization") == "Bearer fixture-caller-key",',
      '                            "model_key": payload["x_agents_core"]["model_provider"]["api_key"] == "fixture-model-key"})',
      `        self.send_response(${status})`,
      '        self.send_header("Location", "http://127.0.0.1:%s/capture" % target.server_port)',
      "        self.end_headers()",
      "    def log_message(self, *args): pass",
      'origin = ThreadingHTTPServer(("127.0.0.1", 0), Origin)',
      "for server in (target, origin):",
      "    threading.Thread(target=server.serve_forever, daemon=True).start()",
      'source = sys.stdin.read().replace("http://127.0.0.1:1/v1/agents", "http://127.0.0.1:%s/v1/agents" % origin.server_port)',
      "rejected_status = None",
      "try:",
      "    with contextlib.redirect_stdout(io.StringIO()):",
      '        exec(compile(source, "generated-request", "exec"), {})',
      "except HTTPError as error:",
      "    rejected_status = error.code",
      "finally:",
      "    for server in (origin, target):",
      "        server.shutdown()",
      "        server.server_close()",
      'print(json.dumps({"status": rejected_status, "origin": origin_hits, "target": target_hits}))',
    ].join("\n");
    const result = JSON.parse(execFileSync("python3", ["-c", fixture], {
      input: source, encoding: "utf8", timeout: 10_000,
      env: { ...process.env, CORE_API_KEY: "fixture-caller-key", MODEL_API_KEY: "fixture-model-key", NO_PROXY: "127.0.0.1" },
    }));
    expect(result).toEqual({ status, origin: [{ caller_key: true, model_key: true }], target: [] });
  });
});
