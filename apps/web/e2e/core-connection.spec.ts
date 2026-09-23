import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const probeRequestPattern = /\/v1\/agents\?limit=1$/;

interface ProbeInstrumentationWindow extends Window {
  __probeAbortCount?: number;
  __probeCallCount?: number;
  __resolveFirstProbe?: (() => void) | null;
  __stalledProbeCallCount?: number;
  __holdNextLocalStartup?: boolean;
  __oldStartupAbortCount?: number;
  __resolveOldStartup?: (() => void) | null;
  __resolveNewStartup?: (() => void) | null;
}

async function resetFixture(request: APIRequestContext) {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/reset`);
  expect(response.ok()).toBe(true);
}

async function boot(page: Page, request: APIRequestContext) {
  await resetFixture(request);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Configure Agent Core connection" })).toBeVisible();
}

async function openConnection(page: Page) {
  const trigger = page.getByRole("button", { name: "Configure Agent Core connection" });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Connect an Agent Core" });
  await expect(dialog).toBeVisible();
  return { dialog, trigger };
}

test("migrates a stale local token without sending browser authorization when proxy auth is disabled", async ({
  page,
  request,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("agents-core-web.core-base-url", "/v1");
    sessionStorage.setItem("agents-core-web.core-token", "historical-local-token");
  });
  let browserAuthorizationSeen = false;
  page.on("request", (browserRequest) => {
    const url = new URL(browserRequest.url());
    if (url.pathname.startsWith("/v1/") && browserRequest.headers().authorization) {
      browserAuthorizationSeen = true;
    }
  });
  await page.route(probeRequestPattern, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ object: "list", data: [], has_more: false, first_id: null, last_id: null }),
  }));

  await boot(page, request);
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("agents-core-web.core-token"))).toBeNull();
  const { dialog } = await openConnection(page);
  await expect(dialog).toContainText("Server-managed key not detected");
  await expect(dialog.getByRole("radio", { name: /Local Parsar Core/ })).toBeChecked();
  await expect(dialog.getByLabel("Bearer token")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Test connection" }).click();
  await expect(dialog.getByRole("status")).toContainText("Core API authenticated");
  expect(browserAuthorizationSeen).toBe(false);

  await dialog.getByRole("radio", { name: /Other compatible Core/ }).click();
  await expect(dialog.getByLabel("Bearer token")).toHaveValue("");
});

test("scrubs a legacy remote HTTP connection before any bearer can leave the page", async ({ page, request }) => {
  await page.addInitScript(() => {
    localStorage.setItem("agents-core-web.core-base-url", "http://core.example/v1");
    sessionStorage.setItem("agents-core-web.core-token", "legacy-remote-token");
  });
  let unsafeRequests = 0;
  await page.route("http://core.example/**", (route) => {
    unsafeRequests += 1;
    return route.abort("blockedbyclient");
  });

  await boot(page, request);

  await expect.poll(() => page.evaluate(() => localStorage.getItem("agents-core-web.core-base-url"))).toBe("/v1");
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("agents-core-web.core-token"))).toBeNull();
  expect(unsafeRequests).toBe(0);
  const { dialog } = await openConnection(page);
  await expect(dialog.getByRole("radio", { name: /Local Parsar Core/ })).toBeChecked();
});

test("rejects an unsafe runtime target before a local proxy write can be attempted", async ({ page, request }) => {
  await boot(page, request);

  const outcome = await page.evaluate(async () => {
    const modulePath = "/src/lib/connection.ts";
    const { createCore } = await import(/* @vite-ignore */ modulePath);
    const originalFetch = window.fetch;
    const calls: Array<{ method: string; url: string }> = [];
    window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        method: init?.method ?? "GET",
        url: typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
      });
      return new Response(JSON.stringify({ id: "must-not-be-created" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    try {
      const core = createCore({
        baseUrl: "http://core.example/v1",
        token: "must-not-leave-browser",
      });
      await core.createAgent({ model: "must-not-create-locally" });
      return { calls, error: null };
    } catch (error) {
      return {
        calls,
        error: error instanceof Error
          ? {
              code: "code" in error ? String(error.code) : null,
              message: error.message,
              name: error.name,
            }
          : null,
      };
    } finally {
      window.fetch = originalFetch;
    }
  });

  expect(outcome.calls).toEqual([]);
  expect(outcome.error).toEqual({
    code: "invalid_core_base_url",
    message: "The Agent Core base URL is invalid or unsafe.",
    name: "InvalidCoreConnectionError",
  });
  expect(JSON.stringify(outcome)).not.toContain("must-not-leave-browser");
  expect(JSON.stringify(outcome)).not.toContain("http://core.example/v1");
});

test("switches real connection modes and fences stale probes when the draft changes or reopens", async ({
  page,
  request,
}) => {
  await page.addInitScript(() => {
    const target = window as ProbeInstrumentationWindow;
    const originalFetch = window.fetch.bind(window);
    let holdFirstProbe = true;
    target.__probeAbortCount = 0;
    target.__probeCallCount = 0;
    target.__resolveFirstProbe = null;

    window.fetch = (input, init) => {
      const url = typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
      const candidate = new URL(url, window.location.href);
      const isProbe = candidate.pathname.endsWith("/v1/agents")
        && candidate.searchParams.get("limit") === "1"
        && [...candidate.searchParams].length === 1;
      if (isProbe) target.__probeCallCount = (target.__probeCallCount ?? 0) + 1;
      if (holdFirstProbe && isProbe) {
        holdFirstProbe = false;
        init?.signal?.addEventListener("abort", () => {
          target.__probeAbortCount = (target.__probeAbortCount ?? 0) + 1;
        }, { once: true });
        return new Promise<Response>((resolve) => {
          target.__resolveFirstProbe = () => resolve(new Response(JSON.stringify({
            object: "list",
            data: [],
            has_more: false,
            first_id: null,
            last_id: null,
          }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }));
        });
      }
      return originalFetch(input, init);
    };
  });
  await boot(page, request);
  const { dialog } = await openConnection(page);
  const local = dialog.getByRole("radio", { name: /Local Parsar Core/ });
  const advanced = dialog.getByRole("radio", { name: /Other compatible Core/ });
  const testConnection = dialog.getByRole("button", { name: "Test connection" });

  await expect(local).toBeChecked();
  await expect(dialog.getByLabel("Compatible Core base URL")).toHaveCount(0);
  await expect(dialog.getByLabel("Bearer token")).toHaveCount(0);
  await testConnection.click();
  await expect(dialog.getByRole("status")).toContainText("Testing Core connection…");
  await expect.poll(() => page.evaluate(() => (
    (window as ProbeInstrumentationWindow).__probeCallCount ?? 0
  ))).toBe(1);
  await expect.poll(() => page.evaluate(() => (
    typeof (window as ProbeInstrumentationWindow).__resolveFirstProbe
  ))).toBe("function");

  await advanced.click();
  await expect(advanced).toBeChecked();
  await expect(dialog.getByRole("status")).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => (
    (window as ProbeInstrumentationWindow).__probeAbortCount ?? 0
  ))).toBe(1);
  await page.evaluate(() => (window as ProbeInstrumentationWindow).__resolveFirstProbe?.());
  await expect(dialog.getByText("Core API authenticated")).toHaveCount(0);

  const directBase = `${new URL(page.url()).origin}/v1`;
  const baseUrl = dialog.getByLabel("Compatible Core base URL");
  const token = dialog.getByLabel("Bearer token");
  for (const loopback of [
    "http://localhost:8091/v1",
    "http://worker.localhost:8091/v1",
    "http://127.0.0.42:8091/v1",
    "http://[::1]:8091/v1",
  ]) {
    await baseUrl.fill(loopback);
    await expect(testConnection).toBeEnabled();
  }
  for (const unsafe of [
    "http://core.example/v1",
    `${directBase}?`,
    `${directBase}#`,
  ]) {
    await baseUrl.fill(unsafe);
    await expect(dialog.getByText(/Enter an HTTPS URL, or an HTTP loopback URL/)).toBeVisible();
    await expect(testConnection).toBeDisabled();
    await expect(dialog.getByRole("button", { name: "Apply connection" })).toBeDisabled();
  }
  expect(await page.evaluate(() => (window as ProbeInstrumentationWindow).__probeCallCount)).toBe(1);

  await baseUrl.fill(directBase);
  await token.fill("current-tab-token");
  await testConnection.click();
  await expect(dialog.getByRole("status")).toContainText("Core API authenticated");

  await token.fill("changed-current-tab-token");
  await expect(dialog.getByRole("status")).toHaveCount(0);
  await testConnection.click();
  await expect(dialog.getByRole("status")).toContainText("Core API authenticated");

  await baseUrl.fill(`${directBase}/`);
  await expect(dialog.getByRole("status")).toHaveCount(0);
  await testConnection.click();
  await expect(dialog.getByRole("status")).toContainText("Core API authenticated");

  await local.click();
  await expect(local).toBeChecked();
  await expect(dialog.getByRole("status")).toHaveCount(0);
  await testConnection.click();
  await expect(dialog.getByRole("status")).toContainText("Core API authenticated");

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog", { name: "Connect an Agent Core" })).toHaveCount(0);
  const reopened = (await openConnection(page)).dialog;
  await expect(reopened.getByRole("radio", { name: /Local Parsar Core/ })).toBeChecked();
  await expect(reopened.getByRole("status")).toHaveCount(0);
  await expect(reopened.getByRole("alert")).toHaveCount(0);
});

test("clears startup configuration synchronously and fences a stale read when the Core changes", async ({
  page,
  request,
}) => {
  await page.addInitScript(() => {
    const target = window as ProbeInstrumentationWindow;
    const originalFetch = window.fetch.bind(window);
    const startupResponse = (defaultHarness: "codex" | "claude_sdk", provider: "docker" | "microsandbox") => new Response(JSON.stringify({
      object: "agents.core.startup_configuration",
      schema_version: 1,
      supported: {
        harnesses: ["claude_sdk", "codex", "mcode"],
        managed_sandbox_providers: ["docker", "microsandbox"],
      },
      configured: {
        default_harness: defaultHarness,
        enabled_harnesses: [defaultHarness],
        daemon_gateway: true,
        self_hosted: true,
        managed_sandbox: { enabled: true, provider, maintenance: false },
        model_providers: [{ harness: defaultHarness, endpoint_configured: true }],
      },
    }), { status: 200, headers: { "Content-Type": "application/json" } });

    target.__holdNextLocalStartup = false;
    target.__oldStartupAbortCount = 0;
    target.__resolveOldStartup = null;
    target.__resolveNewStartup = null;
    window.fetch = (input, init) => {
      const value = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const candidate = new URL(value, window.location.href);
      if (!candidate.pathname.endsWith("/v1/agents/core/startup-configuration")) return originalFetch(input, init);
      const authorization = new Headers(init?.headers).get("Authorization");
      if (target.__holdNextLocalStartup && authorization == null) {
        target.__holdNextLocalStartup = false;
        init?.signal?.addEventListener("abort", () => {
          target.__oldStartupAbortCount = (target.__oldStartupAbortCount ?? 0) + 1;
        }, { once: true });
        return new Promise<Response>((resolve) => {
          target.__resolveOldStartup = () => resolve(startupResponse("codex", "docker"));
        });
      }
      if (authorization === "Bearer replacement-token") {
        return new Promise<Response>((resolve) => {
          target.__resolveNewStartup = () => resolve(startupResponse("claude_sdk", "microsandbox"));
        });
      }
      return originalFetch(input, init);
    };
  });

  await boot(page, request);
  await page.getByRole("button", { name: "System", exact: true }).click();
  const system = page.locator(".system-page");
  await expect(system.locator(".system-harness-card").filter({ hasText: "Codex" })).toContainText("Enabled");
  await page.evaluate(() => {
    (window as ProbeInstrumentationWindow).__holdNextLocalStartup = true;
  });
  await system.getByRole("button", { name: "Refresh System status" }).click();
  await expect.poll(() => page.evaluate(() => typeof (window as ProbeInstrumentationWindow).__resolveOldStartup)).toBe("function");

  const { dialog } = await openConnection(page);
  await dialog.getByRole("radio", { name: /Other compatible Core/ }).click();
  await dialog.getByLabel("Compatible Core base URL").fill(`${new URL(page.url()).origin}/v1`);
  await dialog.getByLabel("Bearer token").fill("replacement-token");
  await dialog.getByRole("button", { name: "Apply connection" }).click();

  const daemonGateway = system.getByRole("listitem").filter({ hasText: "Daemon gateway" });
  await expect(daemonGateway).toContainText("Checking…");
  await expect(system).not.toContainText("Configured for this process");
  await expect.poll(() => page.evaluate(() => (window as ProbeInstrumentationWindow).__oldStartupAbortCount ?? 0)).toBe(1);
  await expect.poll(() => page.evaluate(() => typeof (window as ProbeInstrumentationWindow).__resolveNewStartup)).toBe("function");
  await page.evaluate(() => (window as ProbeInstrumentationWindow).__resolveNewStartup?.());
  await expect(system.locator(".system-harness-card").filter({ hasText: "Claude SDK" })).toContainText("Enabled");
  await expect(system.getByRole("listitem").filter({ hasText: "Managed sandbox" })).toContainText("Microsandbox");

  await page.evaluate(() => (window as ProbeInstrumentationWindow).__resolveOldStartup?.());
  await expect(system.locator(".system-harness-card").filter({ hasText: "Claude SDK" })).toContainText("Enabled");
  await expect(system).not.toContainText("Docker · Maintenance");
});

for (const status of [404, 405]) {
  test(`shows startup configuration as unsupported when an older Core returns ${status}`, async ({ page, request }) => {
    await page.route("**/v1/agents/core/startup-configuration", (route) => route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "unsupported", message: "Unavailable." } }),
    }));
    await boot(page, request);
    await page.getByRole("button", { name: "System", exact: true }).click();

    const system = page.locator(".system-page");
    await expect(system.getByRole("listitem").filter({ hasText: "Daemon gateway" })).toContainText("Not exposed");
    await expect(system).toContainText("This Core version does not expose the startup configuration extension");
    await expect(system).not.toContainText("Configured for this process");
    await expect(system).not.toContainText("Checking…");
  });
}

test("announces loading, authenticated access, and each safe failure state from one GET", async ({
  page,
  request,
}) => {
  const replies = [
    {
      status: 200,
      body: {
        object: "list",
        data: [{
          id: "agent_empty_model",
          object: "agent",
          model: "",
          name: null,
          instructions: null,
          metadata: {},
          multi_agent: { enabled: false, max_concurrent_subagents: null },
          reasoning: {},
          service_tier: "auto",
          text: { format: { type: "text" }, verbosity: "medium" },
          tools: [],
          created_at: 1_700_000_000,
          updated_at: 1_700_000_001,
        }],
        has_more: false,
        first_id: "agent_empty_model",
        last_id: "agent_empty_model",
      },
    },
    { status: 202, body: { object: "list", data: [], has_more: false, first_id: null, last_id: null } },
    { status: 200, body: { object: "list", data: [null], has_more: false, first_id: null, last_id: null } },
    { status: 401, body: { error: { code: "invalid_api_key", message: "safe fixture failure" } } },
    { status: 401, body: { error: { code: "gateway_auth_required", message: "safe fixture failure" } } },
    { status: 400, body: { error: { code: "invalid_beta", message: "safe fixture failure" } } },
    { status: 503, body: { error: { code: "unavailable", message: "safe fixture failure" } } },
    { abort: true },
  ];
  const methods: string[] = [];

  await page.route(probeRequestPattern, async (route) => {
    methods.push(route.request().method());
    const reply = replies.shift();
    if (!reply) return route.abort("failed");
    await new Promise((resolve) => setTimeout(resolve, 150));
    if ("abort" in reply) return route.abort("failed");
    return route.fulfill({
      status: reply.status,
      contentType: "application/json",
      body: JSON.stringify(reply.body),
    });
  });

  await boot(page, request);
  const { dialog } = await openConnection(page);
  const action = dialog.getByRole("button", { name: "Test connection" });
  const cases: Array<{ role: "status" | "alert"; text: string; absentText?: string }> = [
    { role: "status", text: "Core API authenticated" },
    { role: "alert", text: "Agents API protocol mismatch" },
    { role: "alert", text: "Agents API protocol mismatch" },
    { role: "alert", text: "Authentication failed" },
    { role: "alert", text: "Core returned HTTP 401", absentText: "invalid_api_key" },
    { role: "alert", text: "Agents API protocol mismatch" },
    { role: "alert", text: "Core returned HTTP 503" },
    { role: "alert", text: "Core unreachable" },
  ];

  for (const expected of cases) {
    await action.click();
    const loading = dialog.getByRole("status");
    await expect(loading).toHaveAttribute("aria-live", "polite");
    await expect(loading).toContainText("Testing Core connection…");
    const terminal = dialog.getByRole(expected.role);
    await expect(terminal).toContainText(expected.text);
    await expect(terminal).toContainText("Chat uses the current Agents API contract");
    await expect(terminal).toContainText("does not start a Turn or verify its runtime dependencies");
    if (expected.absentText) await expect(terminal).not.toContainText(expected.absentText);
  }

  expect(methods).toEqual(["GET", "GET", "GET", "GET", "GET", "GET", "GET", "GET"]);
  expect(replies).toHaveLength(0);
});

test("turns a stalled probe into one bounded unreachable result", async ({ page, request }) => {
  await page.addInitScript(() => {
    const target = window as ProbeInstrumentationWindow;
    const originalFetch = window.fetch.bind(window);
    target.__stalledProbeCallCount = 0;
    window.fetch = (input, init) => {
      const value = typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url;
      const candidate = new URL(value, window.location.href);
      const stalledProbe = candidate.pathname.endsWith("/v1/agents")
        && candidate.searchParams.get("limit") === "1";
      if (!stalledProbe) return originalFetch(input, init);
      target.__stalledProbeCallCount = (target.__stalledProbeCallCount ?? 0) + 1;
      return new Promise<Response>((_resolve, reject) => {
        const rejectAbort = () => reject(init?.signal?.reason ?? new DOMException("Aborted", "AbortError"));
        if (init?.signal?.aborted) rejectAbort();
        else init?.signal?.addEventListener("abort", rejectAbort, { once: true });
      });
    };
  });

  await boot(page, request);
  const { dialog } = await openConnection(page);
  await dialog.getByRole("button", { name: "Test connection" }).click();
  await expect(dialog.getByRole("status")).toContainText("Testing Core connection…");
  await expect.poll(() => page.evaluate(() => (
    (window as ProbeInstrumentationWindow).__stalledProbeCallCount ?? 0
  ))).toBe(1);
  await expect(dialog.getByRole("alert")).toContainText("Core unreachable", { timeout: 7_500 });
  await expect(dialog.getByRole("alert")).toContainText("Chat uses the current Agents API contract");
  await expect(dialog.getByRole("alert")).toContainText("does not start a Turn or verify its runtime dependencies");
  expect(await page.evaluate(() => (window as ProbeInstrumentationWindow).__stalledProbeCallCount)).toBe(1);
});

test("supports keyboard mode selection, traps focus, and returns focus on Escape", async ({ page, request }) => {
  await boot(page, request);
  const { dialog, trigger } = await openConnection(page);
  const local = dialog.getByRole("radio", { name: /Local Parsar Core/ });
  const advanced = dialog.getByRole("radio", { name: /Other compatible Core/ });
  const apply = dialog.getByRole("button", { name: "Apply connection" });
  const close = dialog.getByRole("button", { name: "Close dialog" });

  await expect(local).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(advanced).toBeChecked();
  await expect(advanced).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(local).toBeChecked();
  await expect(local).toBeFocused();

  await close.focus();
  await page.keyboard.press("Shift+Tab");
  await expect(apply).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();

  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog", { name: "Connect an Agent Core" })).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test("keeps both connection modes operable without horizontal overflow at 390 px", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await boot(page, request);
  const { dialog } = await openConnection(page);
  const apply = dialog.getByRole("button", { name: "Apply connection" });
  const cancel = dialog.getByRole("button", { name: "Cancel" });

  const assertContained = async () => {
    const metrics = await page.evaluate(() => ({
      viewportWidth: innerWidth,
      viewportHeight: innerHeight,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
    }));
    const box = await dialog.boundingBox();
    expect(box).not.toBeNull();
    expect(metrics.documentWidth).toBeLessThanOrEqual(metrics.viewportWidth);
    expect(metrics.bodyWidth).toBeLessThanOrEqual(metrics.viewportWidth);
    expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
    expect(box?.y ?? -1).toBeGreaterThanOrEqual(0);
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(metrics.viewportWidth);
    expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(metrics.viewportHeight);
    const widths = await dialog.evaluate((card) => {
      const body = card.querySelector<HTMLElement>(".modal-body");
      return {
        cardClient: card.clientWidth,
        cardScroll: card.scrollWidth,
        bodyClient: body?.clientWidth ?? 0,
        bodyScroll: body?.scrollWidth ?? 0,
      };
    });
    expect(widths.cardScroll).toBeLessThanOrEqual(widths.cardClient);
    expect(widths.bodyScroll).toBeLessThanOrEqual(widths.bodyClient);
    await expect(apply).toBeInViewport();
    await expect(cancel).toBeInViewport();
  };

  await assertContained();
  const testConnection = dialog.getByRole("button", { name: "Test connection" });
  await testConnection.scrollIntoViewIfNeeded();
  await expect(testConnection).toBeInViewport();

  await dialog.getByRole("radio", { name: /Other compatible Core/ }).click();
  await dialog.getByLabel("Compatible Core base URL").fill(`${new URL(page.url()).origin}/v1`);
  await assertContained();
  await dialog.getByLabel("Compatible Core base URL").scrollIntoViewIfNeeded();
  await expect(dialog.getByLabel("Compatible Core base URL")).toBeInViewport();
});
