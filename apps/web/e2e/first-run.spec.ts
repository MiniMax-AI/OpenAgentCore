import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const username = "console-admin";
const password = "fixture-password-1234";
type AuthStatus = { mode: "setup" | "login" } | { mode: "authenticated"; username: string };

async function fixtureRequests(request: APIRequestContext): Promise<Array<{ method: string; path: string; body?: Record<string, unknown> }>> {
  const response = await request.get(`${fixtureBaseUrl}/__fixture/requests`);
  expect(response.ok()).toBe(true);
  return response.json();
}

async function mockAccount(page: Page, initial: AuthStatus) {
  let status = initial;
  const writes: Array<{ action: string; body: Record<string, string> }> = [];
  await page.route(/\/console\/auth(?:\/(?:setup|login|logout))?$/, async (route) => {
    const request = route.request();
    if (request.method() === "POST") {
      const action = new URL(request.url()).pathname.split("/").at(-1)!;
      writes.push({ action, body: request.postDataJSON() as Record<string, string> });
      status = action === "logout" ? { mode: "login" } : { mode: "authenticated", username };
    }
    await route.fulfill({ json: status });
  });
  return writes;
}

async function fillAccount(page: Page) {
  await page.getByLabel(/^Administrator username/).fill(username);
  await page.getByLabel(/^Password/).fill(password);
}

test.beforeEach(async ({ page, request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
  await page.addInitScript(() => localStorage.setItem("agents-core-web.locale", "en"));
  await page.route("**/console/config", (route) => route.fulfill({ json: { api_keys: true, sandbox_admin: false, node_installer: false } }));
  await page.route("**/console/api-keys", (route) => route.fulfill({ json: { data: [{
    id: "fb533e99-524f-4e44-94bc-8f6e571646a7", name: "Existing terminal key", prefix: "pc_fixture",
    created_at: "2026-09-24T00:00:00Z", revoked_at: null,
  }] } }));
});

test("validates administrator setup before sending credentials and keeps only nonsecret progress", async ({ page }) => {
  const writes = await mockAccount(page, { mode: "setup" });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Create your administrator account" })).toBeVisible();
  await page.getByLabel(/^Setup key/).fill("fixture-setup-secret");
  await fillAccount(page);
  await page.getByLabel("Confirm password", { exact: true }).fill("different-password");
  await page.getByRole("button", { name: "Create administrator account", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("Passwords do not match.");
  expect(writes).toHaveLength(0);
  await page.getByLabel(/^Password/).fill("short");
  await page.getByLabel("Confirm password", { exact: true }).fill("short");
  await page.getByRole("button", { name: "Create administrator account", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("Use a password between 12 and 72 bytes.");
  expect(writes).toHaveLength(0);
  await page.getByLabel(/^Password/).fill(password);
  await page.getByLabel("Confirm password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create administrator account", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Keep your sign-in details." })).toBeVisible();
  expect(writes).toEqual([{ action: "setup", body: { username, password, setup_key: "fixture-setup-secret" } }]);
  await page.getByRole("button", { name: "Copy sign-in details", exact: true }).click();
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  expect(copied).toContain(new URL(page.url()).origin);
  expect(copied).toContain(username);
  expect(copied).not.toContain(password);
  expect(copied).not.toContain("fixture-setup-secret");
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(storage).not.toContain(password);
  expect(storage).not.toContain("fixture-setup-secret");
});

test("remembers the introduction step and dismissal across reloads and sign-in, and allows replay", async ({ page }) => {
  const writes = await mockAccount(page, { mode: "authenticated", username });
  await page.goto("/");
  await page.getByRole("button", { name: "I've saved it. Continue", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Connect your own machine." })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Connect your own machine." })).toBeVisible();
  await page.getByRole("button", { name: "Skip for now", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Make your first API request." })).toBeVisible();
  await page.getByRole("button", { name: "Skip introduction", exact: true }).first().click();
  await expect(page.locator(".first-run-home")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toBeVisible();
  await expect(page.locator(".first-run-home")).toHaveCount(0);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome back", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Configure Agent Core connection" })).toHaveCount(0);
  await fillAccount(page);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toBeVisible();
  await expect(page.locator(".first-run-home")).toHaveCount(0);
  await page.getByRole("button", { name: "Getting started", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Keep your sign-in details." })).toBeVisible();
  expect(writes.map(({ action }) => action)).toEqual(["logout", "login"]);
});

test("keeps private Core requests unmounted on an authentication network failure", async ({ page, request }) => {
  await page.route("**/console/auth", (route) => route.abort("failed"));
  await page.goto("/");
  await expect(page.getByText("Could not connect to your console.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Configure Agent Core connection" })).toHaveCount(0);
  expect(await fixtureRequests(request)).toEqual([]);
  await page.unroute("**/console/auth");
  await mockAccount(page, { mode: "login" });
  await page.getByRole("button", { name: "Try again", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome back", exact: true })).toBeVisible();
  expect(await fixtureRequests(request)).toEqual([]);
});

test("does not replay an uncertain registration and reconciles the account before sign-in", async ({ page }) => {
  let status: AuthStatus = { mode: "setup" };
  let setupWrites = 0;
  await page.route("**/console/auth", (route) => route.fulfill({ json: status }));
  await page.route("**/console/auth/setup", (route) => {
    setupWrites++;
    status = { mode: "login" };
    return route.abort("failed");
  });
  await page.goto("/");
  await page.getByLabel(/^Setup key/).fill("fixture-setup-secret");
  await fillAccount(page);
  await page.getByLabel("Confirm password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create administrator account", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("Could not confirm the result. Check the account status before trying again.");
  await expect(page.getByLabel(/^Password/)).toBeDisabled();
  await expect(page.getByRole("button", { name: "Create administrator account", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Check account status", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Welcome back", exact: true })).toBeVisible();
  expect(setupWrites).toBe(1);
});

test("supports Chinese setup and reduced-motion introduction", async ({ page }) => {
  await mockAccount(page, { mode: "setup" });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.setViewportSize({ width: 1080, height: 900 });
  await page.goto("/");
  await page.getByRole("combobox", { name: "Console language" }).selectOption("zh");
  await expect(page.getByRole("heading", { name: "创建管理员账户" })).toBeVisible();
  await page.getByLabel(/^初始化密钥/).fill("fixture-setup-secret");
  await page.getByLabel(/^管理员用户名/).fill(username);
  await page.getByLabel(/^密码/).fill(password);
  await page.getByLabel("确认密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "创建管理员账户", exact: true }).click();
  await expect(page.getByRole("heading", { name: "保存你的登录信息。" })).toBeVisible();
  await expect(page.locator(".first-run-home")).toHaveAttribute("lang", "zh");
  const layout = await page.locator(".first-run-stage-content").evaluate((element) => ({
    animation: getComputedStyle(element).animationName,
    width: document.documentElement.scrollWidth,
    viewport: innerWidth,
  }));
  expect(layout.animation).toBe("none");
  expect(layout.width).toBeLessThanOrEqual(layout.viewport);
  await page.getByRole("button", { name: /第一个 Agent/ }).click();
  await expect(page.getByRole("heading", { name: "发出第一个 API 请求。" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("runs the copied local request against Core and reveals only its matching Agent", async ({ page, request }) => {
  await mockAccount(page, { mode: "authenticated", username });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/");
  await page.getByRole("button", { name: /Your first Agent/ }).click();
  await page.getByLabel("Agent name", { exact: true }).fill("Ada's first Agent 云");
  await page.getByLabel("Model ID", { exact: true }).fill("fixture/first-run-model");
  await page.getByRole("combobox", { name: "Harness", exact: true }).selectOption("codex");
  await page.getByRole("textbox", { name: "Instructions", exact: true }).fill("Be concise.\nPreserve unicode: 云.");
  await page.getByLabel("Set a model provider for this Agent", { exact: true }).check();
  await page.getByRole("combobox", { name: "Provider protocol", exact: true }).selectOption("responses");
  await page.getByLabel("Provider base URL", { exact: true }).fill("https://models.example/v1");
  await page.getByLabel(/^Model API key/).fill("fixture-typed-model-secret");
  await page.getByLabel("Core API base URL", { exact: true }).fill(`${fixtureBaseUrl}/v1`);
  const code = await page.locator(".first-request-code pre").innerText();
  expect(code).toContain("CORE_API_KEY");
  expect(code).toContain("MODEL_API_KEY");
  expect(code).not.toContain("fixture-typed-model-secret");
  const [form, editor] = await Promise.all([
    page.locator(".first-request-form").boundingBox(), page.locator(".first-request-editor").boundingBox(),
  ]);
  expect(form).not.toBeNull();
  expect(editor).not.toBeNull();
  expect(editor!.x).toBeGreaterThanOrEqual(form!.x + form!.width);
  await page.getByRole("button", { name: "Copy request", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(code);
  await expect(page.getByLabel(/^Model API key/)).toHaveValue("");
  const unrelated = await request.post(`${fixtureBaseUrl}/v1/agents`, {
    headers: { "OpenAI-Beta": "agents=v1" },
    data: { name: "Ada's first Agent 云", model: "fixture/first-run-model", metadata: { core_home_example: "another-request" } },
  });
  expect(unrelated.status()).toBe(201);
  await page.getByRole("button", { name: "Check result", exact: true }).click();
  await expect(page.locator(".first-agent-card")).toHaveCount(0);
  // Execute exactly the generated Python body, with test-only keys supplied outside the code.
  const script = code.split("\n").slice(1, -1).join("\n");
  const outcome = await promisify(execFile)("python3", ["-c", script], {
    env: { ...process.env, CORE_API_KEY: "fixture-external-core-key", MODEL_API_KEY: "fixture-external-model-key" },
    timeout: 10_000,
  });
  expect(outcome.stdout).toMatch(/^Agent created: agent_created_/);
  const createdId = outcome.stdout.trim().split(" ").at(-1)!;
  await page.getByRole("button", { name: "Check result", exact: true }).click();
  await expect(page.locator(".first-agent-card")).toContainText(createdId);
  await expect(page.locator(".first-agent-card")).toContainText("Ada's first Agent 云");
  const writes = (await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(writes).toHaveLength(2);
  expect(writes[1]?.body).toMatchObject({
    name: "Ada's first Agent 云", model: "fixture/first-run-model", instructions: "Be concise.\nPreserve unicode: 云.",
    x_agents_core: { harness: "codex", model_provider: { protocol: "responses", base_url: "https://models.example/v1", api_key: "fixture-external-model-key" } },
  });
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(storage).not.toContain("fixture-typed-model-secret");
  expect(storage).not.toContain("fixture-external-model-key");
  await page.reload();
  await expect(page.locator(".first-agent-card")).toContainText(createdId);
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents")).toHaveLength(2);
  await page.getByRole("button", { name: "Open Agent", exact: true }).click();
  await expect(page.locator(".first-run-home")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Ada's first Agent 云", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Saved definition", exact: true })).toContainText(createdId);
  await page.getByRole("button", { name: "Back to Agents", exact: true }).click();
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await expect(page.locator(".session-page")).toBeVisible();
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await expect(page.getByRole("region", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Saved definition", exact: true })).toHaveCount(0);
});

test("creates one Agent through Run here and clears its transient model key", async ({ page, request }) => {
  await mockAccount(page, { mode: "authenticated", username });
  await page.goto("/");
  await page.getByRole("button", { name: /Your first Agent/ }).click();
  await page.getByLabel("Model ID", { exact: true }).fill("fixture/local-model");
  await page.getByLabel("Set a model provider for this Agent", { exact: true }).check();
  await page.getByLabel("Provider base URL", { exact: true }).fill("https://models.example/v1");
  await expect(page.getByRole("button", { name: "Run here", exact: true })).toBeDisabled();
  await page.getByLabel(/^Model API key/).fill("fixture-local-model-key");
  await page.getByRole("button", { name: "Run here", exact: true }).click();
  await expect(page.locator(".first-agent-card")).toBeVisible();
  await expect(page.getByLabel(/^Model API key/)).toHaveValue("");
  await expect(page.locator(".first-request-code")).not.toContainText("fixture-local-model-key");
  await expect(page.getByRole("button", { name: "Run here", exact: true })).toBeDisabled();
  const writes = (await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(writes).toHaveLength(1);
  expect(writes[0]?.body).toMatchObject({ model: "fixture/local-model", x_agents_core: { model_provider: { api_key: "fixture-local-model-key" } } });
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).not.toContain("fixture-local-model-key");
});

test("requires acknowledgement of a new API key and never recovers its secret from the list", async ({ page }) => {
  await mockAccount(page, { mode: "authenticated", username });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  const secret = "pc_fixture-one-time-key-not-persisted-123456789";
  let metadata: { id: string; name: string; prefix: string; created_at: string; revoked_at: string | null } | null = null;
  let creates = 0;
  let revokes = 0;
  await page.route(/\/console\/api-keys(?:\/[^/]+)?$/, async (route) => {
    const method = route.request().method();
    if (method === "POST") {
      creates++;
      const body = route.request().postDataJSON() as { id: string; name: string };
      metadata = { ...body, prefix: "pc_fixture", created_at: "2026-09-24T00:00:00Z", revoked_at: null };
      return route.fulfill({ json: { ...metadata, key: secret } });
    }
    if (method === "DELETE") {
      revokes++;
      metadata = { ...metadata!, revoked_at: "2026-09-24T00:01:00Z" };
      return route.fulfill({ json: { id: metadata.id, deleted: true } });
    }
    return route.fulfill({ json: { data: metadata ? [metadata] : [] } });
  });
  await page.goto("/");
  const next = page.getByRole("button", { name: "I've saved it. Continue", exact: true });
  await expect(page.getByText("No API keys yet. Create one to get started.", { exact: true })).toBeVisible();
  await expect(next).toBeDisabled();
  await expect(page.getByRole("button", { name: /Your first Agent/ })).toBeDisabled();
  await page.getByLabel("Key name", { exact: true }).fill("Local terminal");
  await page.getByRole("button", { name: "Create API key", exact: true }).click();
  await expect(page.getByLabel("Your new API key", { exact: true })).toHaveValue(secret);
  await expect(next).toBeDisabled();
  await page.getByRole("button", { name: "Copy key", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(secret);
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).not.toContain(secret);
  await expect(next).toBeDisabled();
  await page.getByRole("button", { name: "I've saved this key", exact: true }).click();
  await expect(page.getByLabel("Your new API key", { exact: true })).toHaveCount(0);
  await expect(next).toBeEnabled();
  await page.reload();
  await expect(next).toBeEnabled();
  await expect(page.getByLabel("Your new API key", { exact: true })).toHaveCount(0);
  await expect(page.locator(".api-key-list")).toContainText("Local terminal");
  await expect(page.locator(".api-key-panel")).not.toContainText(secret);
  await page.getByRole("button", { name: "Revoke", exact: true }).click();
  expect(revokes).toBe(0);
  await page.getByRole("button", { name: "Confirm revocation", exact: true }).click();
  await expect(page.locator(".api-key-list")).toContainText("Revoked");
  await expect(next).toBeDisabled();
  expect(creates).toBe(1);
  expect(revokes).toBe(1);
});

test("lets a web-only console use an existing project connection without key management", async ({ page, request }) => {
  await mockAccount(page, { mode: "authenticated", username });
  await page.route("**/console/config", (route) => route.fulfill({ json: { api_keys: false, sandbox_admin: false, node_installer: false } }));
  let keyRequests = 0;
  await page.route("**/console/api-keys{,/**}", (route) => {
    keyRequests++;
    return route.fulfill({ status: 503, json: { error: { message: "Key management is not paired." } } });
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Use an existing Agent API key.", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create API key", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "I've saved it. Continue", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Connect your own machine.", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Skip for now", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Make your first API request.", exact: true })).toBeVisible();
  await page.getByLabel("Model ID", { exact: true }).fill("fixture/web-only-model");
  await page.getByLabel("Set a model provider for this Agent", { exact: true }).uncheck();
  await page.getByRole("button", { name: "Run here", exact: true }).click();
  await expect(page.locator(".first-agent-card")).toBeVisible();
  const writes = (await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(writes).toHaveLength(1);
  expect(writes[0]?.body).toMatchObject({ model: "fixture/web-only-model" });
  expect(keyRequests).toBe(0);
});
