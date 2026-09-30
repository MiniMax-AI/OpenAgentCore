import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { buildDirectory } from "./paths.mjs";
import { openStore, dataPath, AppError } from "./server/store.mjs";
import { productAPI } from "./server/product.mjs";

const root = fileURLToPath(new URL(".", import.meta.url));
const routes = [
  [/^\/v1\/agents\/environments\/[a-f0-9-]{36}$/, ["GET"]],
  [/^\/v1\/agents\/environments\/[a-f0-9-]{36}\/files$/, ["GET", "POST"]],
  [/^\/v1\/agents\/sessions\/[a-f0-9-]{36}\/artifacts$/, ["GET"]],
  [
    /^\/v1\/agents\/sessions\/[a-f0-9-]{36}\/artifacts\/[a-f0-9-]{36}\/content$/,
    ["GET"],
  ],
  [/^\/v1\/agents\/sessions\/[a-f0-9-]{36}$/, ["GET"]],
  [/^\/v1\/agents\/sessions\/[a-f0-9-]{36}\/(items|turns)$/, ["GET"]],
  [/^\/v1\/agents\/sessions\/[a-f0-9-]{36}\/events$/, ["GET", "POST"]],
  [/^\/v1\/agents$/, ["GET", "POST"]],
  [/^\/v1\/agents\/[a-f0-9-]{36}$/, ["GET", "POST"]],
];
routes.push(
  [/^\/v1\/skills$/, ["GET", "POST"]],
  [
    /^\/v1\/skills\/skill_[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/,
    ["GET", "POST", "DELETE"],
  ],
  [
    /^\/v1\/skills\/skill_[A-Za-z0-9][A-Za-z0-9_-]{0,127}\/versions$/,
    ["GET", "POST"],
  ],
);
const mime = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".png": "image/png",
  ".svg": "image/svg+xml",
};

export function configuration(env = process.env) {
  const target = new URL(env.OAC_EXAMPLE_CORE_URL || "http://127.0.0.1:8091");
  if (
    target.username ||
    target.password ||
    target.search ||
    target.hash ||
    target.pathname !== "/" ||
    !(
      target.protocol === "https:" ||
      (target.protocol === "http:" &&
        ["127.0.0.1", "localhost", "[::1]"].includes(target.hostname))
    )
  ) {
    throw new Error(
      "OAC_EXAMPLE_CORE_URL must be an HTTPS origin, or a loopback HTTP origin, without /v1.",
    );
  }
  const key = env.OAC_EXAMPLE_PROJECT_KEY?.trim();
  if (!key || /[\r\n]/.test(key))
    throw new Error("Set OAC_EXAMPLE_PROJECT_KEY to a Project API key.");
  const port = Number(env.OAC_EXAMPLE_PORT || 18180);
  if (!Number.isInteger(port) || port < 1 || port > 65535)
    throw new Error("Invalid OAC_EXAMPLE_PORT.");
  return { target: target.origin, key, port };
}

function fail(res, status, message) {
  res.writeHead(status, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
  });
  res.end(JSON.stringify({ error: { message } }));
}

export function createHandler(
  config,
  { fetchImpl = fetch, frontend, store } = {},
) {
  const product =
    store &&
    productAPI(
      store,
      async (path, method, body, key) => {
        const response = await fetchImpl(`${config.target}${path}`, {
          method,
          body: JSON.stringify(body),
          redirect: "manual",
          signal: AbortSignal.timeout(30_000),
          headers: {
            ...(key ? { "Idempotency-Key": key } : {}),
            Authorization: `Bearer ${config.key}`,
            "OpenAI-Beta": "agents=v1",
            "Content-Type": "application/json",
          },
        });
        if (response.status >= 300 && response.status < 400)
          throw new AppError(502, "Core returned a redirect.");
        const value = await response.json();
        if (!response.ok)
          throw new AppError(
            response.status,
            value.error?.message || "Core request failed.",
          );
        return value;
      },
      fetchImpl,
    );
  return async (req, res) => {
    res.setHeader("X-Content-Type-Options", "nosniff");
    res.setHeader("Referrer-Policy", "no-referrer");
    const hosts = [`127.0.0.1:${config.port}`, `localhost:${config.port}`];
    if (
      !hosts.includes(req.headers.host) ||
      (req.headers.origin &&
        req.headers.origin !== `http://${req.headers.host}`) ||
      req.headers["sec-fetch-site"] === "cross-site"
    ) {
      return fail(
        res,
        403,
        "This local example accepts same-origin requests only.",
      );
    }
    const url = new URL(req.url, `http://${req.headers.host}`);
    if (url.pathname.startsWith("/app/")) {
      if (
        req.method !== "GET" &&
        req.headers.origin !== `http://${req.headers.host}`
      )
        return fail(
          res,
          403,
          "A same-origin Origin header is required for writes.",
        );
      try {
        if (!product) return fail(res, 404, "Product storage unavailable.");
        const chunks = [];
        let size = 0;
        for await (const chunk of req) {
          size += chunk.length;
          if (size > 1024 * 1024) return fail(res, 413, "Request too large.");
          chunks.push(chunk);
        }
        let body = {};
        try {
          if (size) body = JSON.parse(Buffer.concat(chunks));
        } catch {
          throw new AppError(400, "Invalid JSON.");
        }
        if (!body || typeof body !== "object" || Array.isArray(body))
          throw new AppError(400, "Invalid request.");
        const value = await product(req.method, url.pathname, body);
        res.writeHead(200, {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
        });
        res.end(JSON.stringify(value));
      } catch (error) {
        fail(
          res,
          error instanceof AppError ? error.status : 502,
          error instanceof AppError ? error.message : "无法完成请求，请重试。",
        );
      }
      return;
    }
    if (
      url.pathname.startsWith("/v1/") ||
      url.pathname.startsWith("/core/") ||
      url.pathname.startsWith("/api/")
    ) {
      if (
        !routes.some(
          ([pattern, methods]) =>
            pattern.test(url.pathname) && methods.includes(req.method),
        )
      ) {
        return fail(res, 404, "This example does not expose that route.");
      }
      if (
        req.method !== "GET" &&
        req.headers.origin !== `http://${req.headers.host}`
      ) {
        return fail(
          res,
          403,
          "A same-origin Origin header is required for writes.",
        );
      }
      const streaming =
        req.method === "GET" && url.pathname.endsWith("/events");
      const controller = new AbortController();
      const fileWrite =
        req.method === "POST" && url.pathname.endsWith("/files");
      const timer = setTimeout(
        () => controller.abort(),
        fileWrite ? 240_000 : 30_000,
      );
      res.on("close", () => controller.abort());
      try {
        const chunks = [];
        let size = 0;
        for await (const chunk of req) {
          size += chunk.length;
          if (
            size >
            (url.pathname.startsWith("/v1/skills") ||
            url.pathname.endsWith("/files")
              ? 8 * 1024 * 1024
              : 1024 * 1024)
          )
            return fail(res, 413, "Request too large.");
          chunks.push(chunk);
        }
        const headers = {
          Authorization: `Bearer ${config.key}`,
          "OpenAI-Beta": "agents=v1",
          "Content-Type": req.headers["content-type"] || "application/json",
          ...(streaming ? { Accept: "text/event-stream" } : {}),
        };
        if (url.pathname.startsWith("/v1/skills"))
          delete headers["OpenAI-Beta"];
        if (req.headers["idempotency-key"])
          headers["Idempotency-Key"] = req.headers["idempotency-key"];
        const upstream = await fetchImpl(
          `${config.target}${url.pathname}${url.search}`,
          {
            method: req.method,
            headers,
            redirect: "manual",
            signal: controller.signal,
            ...(req.method === "POST" ? { body: Buffer.concat(chunks) } : {}),
          },
        );
        if (upstream.status >= 300 && upstream.status < 400)
          return fail(
            res,
            502,
            "Core returned a redirect; check its configured origin.",
          );
        if (streaming && upstream.ok) {
          if (
            !upstream.headers
              .get("content-type")
              ?.startsWith("text/event-stream") ||
            !upstream.body
          )
            return fail(res, 502, "Core did not return an event stream.");
          clearTimeout(timer);
          res.writeHead(200, {
            "Content-Type": "text/event-stream",
            "Cache-Control": "no-cache, no-store",
            "X-Accel-Buffering": "no",
          });
          res.flushHeaders();
          await pipeline(Readable.fromWeb(upstream.body), res);
          return;
        }
        if (url.pathname.endsWith("/content") && upstream.ok && upstream.body) {
          // Downloads stay binary and are never rendered as active browser content.
          clearTimeout(timer);
          res.writeHead(upstream.status, {
            "Content-Type": "application/octet-stream",
            "Content-Disposition":
              upstream.headers.get("content-disposition") || "attachment",
            "Cache-Control": "no-store",
          });
          await pipeline(Readable.fromWeb(upstream.body), res);
          return;
        }
        const body = await upstream.text();
        res.writeHead(upstream.status, {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
        });
        res.end(body);
      } catch {
        if (res.headersSent) {
          res.destroy();
          return;
        }
        if (!res.destroyed)
          fail(
            res,
            502,
            "Cannot reach Core. Check the example server configuration and retry the same request.",
          );
      } finally {
        clearTimeout(timer);
      }
      return;
    }
    if (!["GET", "HEAD"].includes(req.method))
      return fail(res, 405, "Method not allowed.");
    if (frontend) return frontend(req, res);
    try {
      const path = resolve(
        buildDirectory,
        `.${decodeURIComponent(url.pathname)}`,
      );
      if (
        !path.startsWith(`${buildDirectory}${sep}`) &&
        path !== buildDirectory
      )
        return fail(res, 404, "Not found.");
      const asset = extname(path)
        ? path
        : resolve(buildDirectory, "index.html");
      const content = await readFile(asset);
      res.writeHead(200, {
        "Content-Type": mime[extname(asset)] || "application/octet-stream",
        "Cache-Control": "no-cache",
      });
      res.end(req.method === "HEAD" ? undefined : content);
    } catch {
      fail(res, 404, "Page not found. Run the example build first.");
    }
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const config = configuration();
  const server = createServer();
  const vite = process.argv.includes("--dev")
    ? await (
        await import("vite")
      ).createServer({
        root,
        server: { middlewareMode: true, hmr: { server } },
        appType: "spa",
      })
    : undefined;
  const store = openStore(dataPath(config));
  server.on(
    "request",
    createHandler(config, { frontend: vite?.middlewares, store }),
  );
  server.listen(config.port, "127.0.0.1", () => {
    process.stdout.write(`Parsar: http://127.0.0.1:${config.port}\n`);
  });
  for (const signal of ["SIGINT", "SIGTERM"])
    process.on(signal, () => {
      server.close(() => store.close());
      void vite?.close();
    });
}
