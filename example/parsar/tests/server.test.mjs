import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer, request } from "node:http";
import { configuration, createHandler } from "../server.mjs";

test("oversized writes return a definite 413 without contacting Core", async (t) => {
  let called = false;
  const url = await serve(t, () => {
    called = true;
    return Response.json({});
  });
  const response = await fetch(`${url}/v1/agents`, {
    method: "POST",
    headers: { origin: url },
    body: "x".repeat(1024 * 1024 + 1),
  });
  assert.equal(response.status, 413);
  assert.equal(called, false);
});

async function serve(t, fetchImpl) {
  const config = {
    port: 0,
    key: "server-only-secret",
    target: "https://core.example",
  };
  const server = createServer(createHandler(config, { fetchImpl }));
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  config.port = server.address().port;
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  return `http://127.0.0.1:${config.port}`;
}

test("server injects only the project key and preserves request identity", async (t) => {
  let called = false;
  const url = await serve(t, async (target, init) => {
    called = true;
    assert.equal(target, "https://core.example/v1/agents");
    assert.equal(init.headers.Authorization, "Bearer server-only-secret");
    assert.equal(init.headers["Idempotency-Key"], "same-operation");
    assert.equal(init.headers["OpenAI-Beta"], "agents=v1");
    assert.equal(init.redirect, "manual");
    assert.equal(init.headers.cookie, undefined);
    return Response.json({ id: "session" }, { status: 201 });
  });
  const result = await fetch(`${url}/v1/agents`, {
    method: "POST",
    body: "{}",
    headers: {
      origin: url,
      authorization: "Bearer browser-secret",
      cookie: "secret=cookie",
      "idempotency-key": "same-operation",
    },
  });
  assert.equal(result.status, 201);
  assert.equal(called, true);
  assert.equal((await result.text()).includes("secret"), false);
});

test("cross-origin, rebinding, management and encoded paths never reach Core", async (t) => {
  const url = await serve(t, () => {
    throw new Error("must not reach Core");
  });
  for (const [path, headers, method, status] of [
    ["/v1/agents", { origin: "https://evil.example" }, "GET", 403],
    ["/v1/agents", { host: "evil.example" }, "GET", 403],
    ["/v1/agents", { "sec-fetch-site": "cross-site" }, "GET", 403],
    ["/v1/agents", {}, "POST", 403],
    ["/core/v1/projects", {}, "GET", 404],
    ["/api/v1/devices", {}, "GET", 404],
    ["/v1/agents%2fsessions", {}, "GET", 404],
    ["/v1/agents", {}, "DELETE", 404],
  ]) {
    const received = await new Promise((done, reject) => {
      const req = request(`${url}${path}`, { method, headers }, (res) => {
        res.resume();
        done(res.statusCode);
      });
      req.on("error", reject);
      req.end();
    });
    assert.equal(
      received,
      status,
      `${method} ${path} ${JSON.stringify(headers)}`,
    );
  }
});

test("redirects and transport errors do not expose credentials or follow another host", async (t) => {
  for (const reply of [
    () =>
      new Response(null, {
        status: 302,
        headers: { location: "https://evil.example" },
      }),
    () => {
      throw new Error("server-only-secret");
    },
  ]) {
    const url = await serve(t, reply);
    const result = await fetch(`${url}/v1/agents`);
    assert.equal(result.status, 502);
    assert.equal((await result.text()).includes("server-only-secret"), false);
  }
});

test("configuration keeps remote credentials on HTTPS and requires a project key", () => {
  for (const target of [
    "http://remote.example",
    "https://user:pass@core.example",
    "https://core.example/",
    "https://core.example/?token=x",
  ]) {
    assert.throws(() =>
      configuration({
        OPENAI_BASE_URL: target,
        OPENAI_API_KEY: "key",
      }),
    );
  }
  assert.throws(() => configuration({}));
  assert.equal(
    configuration({ OPENAI_API_KEY: "key" }).target,
    "http://127.0.0.1:8091",
  );
});

test("SSE forwards chunks before completion and aborts upstream when the browser disconnects", async (t) => {
  let upstreamSignal;
  const url = await serve(t, async (_target, init) => {
    assert.equal(init.headers.Accept, "text/event-stream");
    upstreamSignal = init.signal;
    return new Response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(
            new TextEncoder().encode('data: {"delta":"first"}\n\n'),
          );
        },
      }),
      { headers: { "Content-Type": "text/event-stream" } },
    );
  });
  const abort = new AbortController();
  const response = await fetch(
    `${url}/v1/agents/sessions/00000000-0000-4000-8000-000000000001/events`,
    { signal: abort.signal },
  );
  assert.equal(response.headers.get("Content-Type"), "text/event-stream");
  const read = await response.body.getReader().read();
  assert.match(new TextDecoder().decode(read.value), /first/);
  const disconnected = new Promise((resolve) =>
    upstreamSignal.addEventListener("abort", resolve, { once: true }),
  );
  abort.abort();
  await disconnected;
});

test("workspace uploads allow bounded inline files and artifact downloads preserve binary bytes", async (t) => {
  const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const binary = new Uint8Array([0, 255, 128, 13, 10]);
  let writes = 0;
  const url = await serve(t, (target, init) => {
    assert.equal(init.headers.Authorization, "Bearer server-only-secret");
    if (target.endsWith("/files")) {
      writes++;
      assert.ok(init.body.length > 1024 * 1024);
      return Response.json({ ok: true }, { status: 201 });
    }
    return new Response(binary, {
      headers: {
        "Content-Type": "text/html",
        "Content-Disposition": 'attachment; filename="report.bin"',
      },
    });
  });
  const uploaded = await fetch(`${url}/v1/agents/environments/${id}/files`, {
    method: "POST",
    headers: { origin: url, "content-type": "application/json" },
    body: JSON.stringify({
      type: "inline",
      path: "/workspace/inputs/a",
      data: Buffer.alloc(1024 * 1024).toString("base64"),
    }),
  });
  assert.equal(uploaded.status, 201);
  const tooLarge = await fetch(`${url}/v1/agents/environments/${id}/files`, {
    method: "POST",
    headers: { origin: url },
    body: "x".repeat(8 * 1024 * 1024 + 1),
  });
  assert.equal(tooLarge.status, 413);
  assert.equal(writes, 1);
  const download = await fetch(
    `${url}/v1/agents/sessions/${id}/artifacts/${id}/content`,
  );
  assert.equal(
    download.headers.get("content-type"),
    "application/octet-stream",
  );
  assert.match(download.headers.get("content-disposition"), /^attachment/);
  assert.deepEqual(new Uint8Array(await download.arrayBuffer()), binary);
  const denied = await fetch(
    `${url}/v1/agents/sessions/${id}/artifacts/${id}/content`,
    { headers: { origin: "https://other.example" } },
  );
  assert.equal(denied.status, 403);
});

test("file writes can finish beyond the ordinary proxy timeout", async (t) => {
  const url = await serve(t, async (_target, init) => {
    await new Promise((resolve) => setTimeout(resolve, 31_000));
    assert.equal(init.signal.aborted, false);
    return Response.json({ path: "/workspace/inputs/file.txt" });
  });
  const response = await fetch(
    `${url}/v1/agents/environments/00000000-0000-4000-8000-000000000001/files`,
    {
      method: "POST",
      headers: { origin: url },
      body: "{}",
    },
  );
  assert.equal(response.status, 200);
});

test("OAuth callback navigation serves only the local frontend while cross-site API access stays forbidden", async (t) => {
  let frontend = 0;
  const config = { port: 0, key: "secret", target: "https://core.example" };
  const server = createServer(
    createHandler(config, {
      frontend: (_req, res) => {
        frontend++;
        res.end("callback page");
      },
    }),
  );
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  config.port = server.address().port;
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const origin = `http://127.0.0.1:${config.port}`;
  const response = await fetch(
    `${origin}/oauth/callback?state=opaque&code=private-code`,
    { headers: { "sec-fetch-site": "cross-site" } },
  );
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("referrer-policy"), "no-referrer");
  assert.equal(frontend, 1);
  assert.equal(
    (
      await fetch(`${origin}/app/mcp-oauth/anything`, {
        method: "POST",
        headers: { "sec-fetch-site": "cross-site" },
      })
    ).status,
    403,
  );
  const rebound = await new Promise((resolve) => {
    const req = request(
      `${origin}/oauth/callback`,
      { headers: { host: "evil.example", "sec-fetch-site": "cross-site" } },
      (response) => {
        response.resume();
        resolve(response.statusCode);
      },
    );
    req.end();
  });
  assert.equal(rebound, 403);
});

test("external links may navigate to the homepage without permitting cross-site API access", async (t) => {
  const config = { port: 0, key: "secret", target: "https://core.example" };
  const server = createServer(
    createHandler(config, {
      frontend: (_req, res) => res.end("Parsar shell"),
      fetchImpl: () => {
        throw new Error("must not reach Core");
      },
    }),
  );
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  config.port = server.address().port;
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const origin = `http://127.0.0.1:${config.port}`;
  const navigation = {
    "sec-fetch-site": "cross-site",
    "sec-fetch-mode": "navigate",
    "sec-fetch-dest": "document",
  };
  for (const [path, method, headers, status] of [
    ["/", "GET", navigation, 200],
    ["/?from=chat", "GET", navigation, 200],
    ["/index.html", "GET", navigation, 200],
    ["/app/agents", "GET", navigation, 403],
    ["/v1/agents/sessions", "GET", navigation, 403],
    ["/", "POST", navigation, 403],
    [
      "/",
      "GET",
      { ...navigation, "sec-fetch-mode": "cors", "sec-fetch-dest": "empty" },
      403,
    ],
    ["/", "GET", { ...navigation, "sec-fetch-dest": "iframe" }, 403],
    ["/", "GET", { ...navigation, host: "evil.example" }, 403],
    ["/", "GET", { ...navigation, origin: "https://evil.example" }, 403],
  ]) {
    const actual = await new Promise((resolve, reject) => {
      const req = request(`${origin}${path}`, { method, headers }, (res) => {
        res.resume();
        resolve(res.statusCode);
      });
      req.on("error", reject);
      req.end();
    });
    assert.equal(
      actual,
      status,
      `${method} ${path} ${JSON.stringify(headers)}`,
    );
  }
});

test("loopback port forwarding preserves exact-origin writes and rejects non-loopback Hosts", async (t) => {
  let calls = 0;
  const url = await serve(t, async () => {
    calls++;
    return Response.json({ id: "session" });
  });
  for (const [host, origin, status] of [
    ["localhost:57357", "http://localhost:57357", 200],
    ["127.0.0.1:57357", "http://127.0.0.1:57357", 200],
    ["localhost", "http://localhost", 200],
    ["localhost:57357", "http://localhost:18180", 403],
    ["localhost:57357", "http://127.0.0.1:57357", 403],
    ["localhost:57357", "https://evil.example", 403],
    ["localhost:57357", undefined, 403],
    [
      "localhost.evil.example:57357",
      "http://localhost.evil.example:57357",
      403,
    ],
    ["evil.example:57357", "http://evil.example:57357", 403],
    ["0.0.0.0:57357", "http://0.0.0.0:57357", 403],
    ["localhost:65536", "http://localhost:65536", 403],
    ["localhost:0", "http://localhost:0", 403],
  ]) {
    const actual = await new Promise((resolve, reject) => {
      const req = request(
        `${url}/v1/agents`,
        { method: "POST", headers: { host, ...(origin ? { origin } : {}) } },
        (res) => {
          res.resume();
          resolve(res.statusCode);
        },
      );
      req.on("error", reject);
      req.end("{}");
    });
    assert.equal(actual, status, `${host} / ${origin}`);
  }
  assert.equal(calls, 3);
});
