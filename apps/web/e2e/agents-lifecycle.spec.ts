import { expect, test, type APIRequestContext, type Locator, type Page, type TestInfo } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;

interface FixtureRequest {
  method: string;
  path: string;
  query?: string;
  beta: string | null;
  authorizationPresent: boolean;
  idempotencyKeyPresent: boolean;
  idempotencyKey: string | null;
  body?: Record<string, unknown>;
}

async function resetFixture(request: APIRequestContext) {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/reset`);
  expect(response.ok()).toBe(true);
}

async function controlFixture(request: APIRequestContext, control: Record<string, unknown>) {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/control`, { data: control });
  expect(response.ok()).toBe(true);
}

interface FixtureState {
  sessions: Array<{ id: string; metadata: Record<string, string> }>;
  aborts: { sessionListReads: number; sessionReads: number; itemReads: number; turnReads: number; streams: number; environmentFileReads: number };
  openStreams: string[];
}

async function fixtureState(request: APIRequestContext): Promise<FixtureState> {
  const response = await request.get(`${fixtureBaseUrl}/__fixture/state`);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<FixtureState>;
}

async function setFixtureSessionMetadata(
  request: APIRequestContext,
  id: string,
  metadata: Record<string, string>,
) {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/session-metadata`, { data: { id, metadata } });
  expect(response.ok()).toBe(true);
}

async function removeFixtureSession(request: APIRequestContext, id: string) {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/remove-session`, { data: { id } });
  expect(response.ok()).toBe(true);
}

async function emitTurnFixture(request: APIRequestContext, status: "completed" | "failed" | "cancelled") {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/emit-turn`, { data: { status } });
  expect(response.ok()).toBe(true);
}

async function emitSessionFixture(request: APIRequestContext, status: "in_progress" | "idle" | "failed") {
  const response = await request.post(`${fixtureBaseUrl}/__fixture/emit-session`, { data: { status } });
  expect(response.ok()).toBe(true);
}

async function fixtureRequests(request: APIRequestContext): Promise<FixtureRequest[]> {
  const response = await request.get(`${fixtureBaseUrl}/__fixture/requests`);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<FixtureRequest[]>;
}

async function createFixtureSession(request: APIRequestContext, label: string) {
  const response = await request.post(`${fixtureBaseUrl}/v1/agents/sessions`, {
    headers: {
      "OpenAI-Beta": "agents=v1",
      "Idempotency-Key": `fixture-filter-${label}`,
    },
    data: {
      agent_id: "agent_b",
      environment: { type: "none" },
      metadata: { filter_fixture: label },
      input: `Review the filter fixture ${label}.`,
      stream: false,
      vault_ids: [],
    },
  });
  expect(response.status()).toBe(201);
}

async function expectSelectedDeleteAbortsSessionRead(
  page: Page,
  request: APIRequestContext,
  startRead: () => Promise<unknown>,
) {
  const path = "/v1/agents/sessions/session_snapshot";
  const failedReads = new Map<string, string>();
  page.on("requestfailed", (failedRequest) => {
    const failedPath = new URL(failedRequest.url()).pathname;
    if (failedRequest.method() === "GET") {
      failedReads.set(failedPath, failedRequest.failure()?.errorText ?? "unknown failure");
    }
  });
  const before = await fixtureState(request);
  const previousReads = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === path
  )).length;

  await startRead();
  await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === path
  )).length).toBeGreaterThan(previousReads);

  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog).toHaveCount(0);

  await expect.poll(() => failedReads.get(path)).toContain("ERR_ABORTED");
  await expect.poll(async () => (await fixtureState(request)).aborts.sessionReads)
    .toBeGreaterThan(before.aborts.sessionReads);
  expect((await fixtureState(request)).sessions.some((session) => session.id === "session_snapshot")).toBe(false);
}

/**
 * Single-Environment reads only. The reusable Template collection shares this
 * path prefix but is durable configuration, not an Environment resource read.
 */
function isEnvironmentResourcePath(path: string): boolean {
  return path.startsWith("/v1/agents/environments/") && !path.startsWith("/v1/agents/environments/templates");
}

async function openAgents(page: Page, request: APIRequestContext) {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Agents" }).click();
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
}

async function startSessionWithSecondAgent(page: Page) {
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("Review the selected Agent configuration.");
  const liveHandoff = page.waitForResponse((response) => (
    response.request().method() === "GET" && /\/agents\/sessions\/session_created_[^/]+\/events$/u.test(new URL(response.url()).pathname)
  ));
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await liveHandoff;
}

async function openAdvancedSessionSettings(dialog: Locator) {
  const toggle = dialog.getByRole("button", { name: /Advanced settings/ });
  if (await toggle.getAttribute("aria-expanded") !== "true") await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(dialog.getByRole("region", { name: "Advanced Session settings" })).toBeVisible();
}

function environmentTrigger(page: Page) {
  return page.getByRole("button", { name: /Environment|Connect environment/i });
}

function connectedLiveEvents(page: Page) {
  return page.getByRole("status", { name: "Session live events: connected" });
}

async function openEnvironmentDialog(page: Page) {
  const conversation = page.getByRole("tabpanel", { name: "Conversation" });
  await expect(conversation.getByRole("region", { name: "Environment and Workspace status" })).toHaveCount(0);

  const trigger = environmentTrigger(page);
  await expect(trigger).toBeVisible();
  await expect(trigger).toHaveAttribute("aria-haspopup", "dialog");
  await trigger.click();

  const dialog = page.getByRole("dialog", { name: "Environment" });
  await expect(dialog).toBeVisible();
  const panel = dialog.getByRole("region", { name: "Environment and Workspace status" });
  await expect(panel).toBeVisible();
  return { dialog, panel, trigger };
}

async function attachScreenshot(page: Page, testInfo: TestInfo, name: string) {
  await testInfo.attach(name, {
    body: await page.screenshot({ fullPage: true, animations: "disabled" }),
    contentType: "image/png",
  });
}

async function attachElementScreenshot(locator: Locator, testInfo: TestInfo, name: string) {
  await testInfo.attach(name, {
    body: await locator.screenshot({ animations: "disabled" }),
    contentType: "image/png",
  });
}

async function openSessionsFromHome(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
}

test("opens Dashboard as the default landing page", async ({ page, request }) => {
  await resetFixture(request);
  await page.goto("/");

  await expect(page.getByRole("button", { name: "Dashboard", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { name: "Dashboard", exact: true })).toBeVisible();
  await expect(page.locator(".dashboard-page")).toBeVisible();
});

test("explains a 502 Core backend failure and opens copyable Docker recovery steps", async ({ page }) => {
  await page.route("**/v1/agents**", async (route) => {
    await route.fulfill({
      status: 502,
      contentType: "application/json",
      body: JSON.stringify({
        error: { message: "Agent core request failed (502).", type: "gateway_error" },
      }),
    });
  });
  await page.goto("/");

  const recovery = page.getByRole("button", {
    name: "Agent Core backend is not ready. Open Docker startup guide",
  });
  await expect(recovery).toBeVisible();
  await expect(recovery).toContainText("HTTP 502");
  await expect(page.getByText("Agents: Agent core request failed (502).", { exact: true })).toHaveCount(0);
  await expect(page.getByText(
    "Agent Core backend is not ready. Open the Dashboard startup guide.",
    { exact: true },
  )).toHaveCount(1);
  await expect(page.getByText("Agent core request failed (502).", { exact: true })).toHaveCount(0);
  await recovery.click();

  const dialog = page.getByRole("dialog", { name: "Connect an Agent Core" });
  await expect(dialog).toContainText("Start a local Docker backend");
  await expect(dialog).toContainText("docker start parsar-agents-api-web-smoke-db");
  await expect(dialog).toContainText("docker start agents-core-web-api agents-core-web-daemon");
  await expect(dialog).toContainText("http://127.0.0.1:8091/healthz");
  await expect(dialog).toContainText("First time on this computer");
  await expect(dialog).toContainText("make docker-build-agents-api");
  await expect(dialog.getByRole("link", { name: "Core container setup" })).toHaveAttribute(
    "href",
    /parsar-core\/blob\/main\/services\/agents-api\/CONTAINER\.md$/,
  );
  await expect(dialog.getByRole("button", { name: "Copy database start command" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Copy Core image build command" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Test connection" })).toBeVisible();
});

test("retrieves the latest Agent and opens its validated edit setup directly", async ({ page, request }, testInfo) => {
  const browserErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });
  page.on("pageerror", (error) => browserErrors.push(error.message));

  await openAgents(page, request);
  await expect(page.getByText("Lifecycle Agent · stale list", { exact: true })).toBeVisible();

  await controlFixture(request, { retrieveDelayMs: 250 });
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  await expect(page.getByRole("status")).toHaveText("Opening latest definition…");
  const setup = page.locator(".agent-setup-page");
  await expect(setup.getByRole("heading", { name: "Lifecycle Agent", exact: true })).toBeVisible();
  await expect(setup).not.toContainText("stale list");
  await expect(setup).toContainText("agent_a");
  await expect(setup.getByRole("heading", { name: "Saved definition" })).toBeVisible();
  await expect(setup).toContainText("Start Session is unavailable. Current Core Session admission requires");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(setup.getByRole("button", { name: "Save changes" })).toBeInViewport();
  expect(await setup.locator(".agent-setup-actions").evaluate((element) => getComputedStyle(element).position)).toBe("sticky");
  await attachScreenshot(page, testInfo, "desktop-light-agent-edit-setup");

  const name = page.getByLabel("Name");
  await expect(name).toBeFocused();
  await expect(setup).toContainText("Web-side suggestions, not a discovered Core catalog");
  await expect(setup).toContainText("Never store secrets in Agent metadata");

  await page.getByLabel("Metadata").fill('{"retries":3}');
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Every metadata value must be a string.")).toBeVisible();
  let requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/agent_a")).toHaveLength(0);

  await page.getByLabel("Metadata").fill(JSON.stringify(Object.fromEntries(
    Array.from({ length: 17 }, (_, index) => [`key-${index}`, "value"]),
  )));
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Agent metadata supports at most 16 pairs.")).toBeVisible();
  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/agent_a")).toHaveLength(0);

  await name.fill("");
  await page.getByLabel("Instructions").fill("");
  await page.getByLabel("Metadata").fill('{"team":"acceptance"}');
  await page.getByLabel("Reasoning effort").selectOption("");
  await page.getByLabel("Reasoning summary").selectOption("");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("status")).toContainText("Agent definition updated");
  await expect(name).toBeFocused();

  requests = await fixtureRequests(request);
  const updates = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/agent_a");
  expect(updates).toHaveLength(1);
  expect(updates[0]?.body).toMatchObject({
    name: null,
    instructions: null,
    metadata: { team: "acceptance" },
    reasoning: { effort: null, summary: null },
  });
  expect(updates[0]?.body).not.toHaveProperty("tools");
  expect(browserErrors).toEqual([]);
});

test("fails closed when the latest Agent response has a different identity", async ({ page, request }) => {
  await openAgents(page, request);
  await page.route("**/v1/agents/agent_a", async (route) => {
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    const upstream = await route.fetch();
    const payload = await upstream.json() as Record<string, unknown>;
    await route.fulfill({ response: upstream, json: { ...payload, id: "agent_other" } });
  });

  const editTrigger = page.getByRole("button", { name: /^Edit Lifecycle Agent/ });
  await editTrigger.click();
  await expect(page.getByRole("alert")).toContainText("returned a different Agent than the one requested");
  await expect(page.locator(".agent-setup-page")).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
  await expect(editTrigger).toBeFocused();

  const reads = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/agent_a"
  ));
  expect(reads).toHaveLength(1);
});

test("creates, previews, edits, and removes bounded Function and anonymous HTTP MCP tools", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: /^Create agent/ }).click();
  await page.getByLabel("Name").fill("Tool Agent");
  await page.getByLabel("Instructions").fill("Use only the configured tools.");
  await page.getByLabel("Model").selectOption({ label: "Custom model ID…" });
  await page.getByLabel("Custom model ID").fill("fixture/tool-model");

  await page.getByRole("button", { name: "Add Function" }).click();
  const createFunction = page.locator(".agent-tool-card").filter({ hasText: "Function" }).first();
  await createFunction.getByLabel("Name", { exact: true }).fill("lookup_customer");
  await createFunction.getByLabel("Description", { exact: true }).fill("Look up a customer.");
  await createFunction.getByRole("textbox", { name: /^Parameters JSON Schema/ }).fill('{"type":"object","properties":{"id":{"type":"string"}}}');

  await page.getByRole("button", { name: "Add HTTP MCP" }).click();
  const createMcp = page.locator(".agent-tool-card").filter({ hasText: "Anonymous HTTP MCP" }).first();
  await createMcp.getByLabel("Server label").fill("docs");
  await createMcp.getByLabel("Server URL").fill("https://mcp.example/tools");
  await createMcp.getByLabel("Only the listed tools").check();
  await createMcp.getByLabel("Allowed tools for docs").fill("search\nread_document");
  await createMcp.getByLabel("Require this server for Core execution").check();

  const previewBody = page.getByRole("region", { name: "Request preview" })
    .locator(".agent-preview-block")
    .filter({ has: page.getByText("agent.json", { exact: true }) })
    .locator("pre");
  await expect(previewBody).toContainText('"type": "function"');
  await expect(previewBody).toContainText('"name": "lookup_customer"');
  await expect(previewBody).toContainText('"type": "mcp"');
  await expect(previewBody).toContainText('"required": true');

  await page.getByRole("button", { name: "Save Agent definition" }).click();
  await expect(page.getByRole("status")).toContainText("Agent definition saved as");
  let requests = await fixtureRequests(request);
  const creates = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(creates).toHaveLength(1);
  expect(creates[0]?.body).toEqual({
    model: "fixture/tool-model",
    name: "Tool Agent",
    instructions: "Use only the configured tools.",
    metadata: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: [
      {
        type: "function",
        name: "lookup_customer",
        description: "Look up a customer.",
        parameters: { type: "object", properties: { id: { type: "string" } } },
        defer_loading: false,
      },
      {
        type: "mcp",
        server_label: "docs",
        transport: { type: "http", server_url: "https://mcp.example/tools" },
        allowed_tools: ["search", "read_document"],
        connection_origin: "service",
        required: true,
      },
    ],
  });

  await page.getByRole("button", { name: "Back to Agents" }).click();
  await page.getByRole("button", { name: /^Edit Tool Agent/ }).click();
  const setup = page.locator(".agent-setup-page");
  await expect(setup.getByRole("heading", { name: "Saved definition" })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const editFunction = setup.locator(".agent-tool-card").filter({ hasText: "Function" }).first();
  await editFunction.getByLabel("Name", { exact: true }).fill("lookup_customer_v2");
  const editMcp = setup.locator(".agent-tool-card").filter({ hasText: "Anonymous HTTP MCP" }).first();
  await editMcp.getByRole("button", { name: "Remove" }).click();
  await setup.getByRole("button", { name: "Save changes" }).click();
  await expect(setup.getByRole("status")).toContainText("Agent definition updated");

  requests = await fixtureRequests(request);
  let updates = requests.filter((entry) => entry.method === "POST" && entry.path.startsWith("/v1/agents/agent_created_"));
  expect(updates).toHaveLength(1);
  expect(updates[0]?.body?.tools).toEqual([
    {
      type: "function",
      name: "lookup_customer_v2",
      description: "Look up a customer.",
      parameters: { type: "object", properties: { id: { type: "string" } } },
      defer_loading: false,
    },
  ]);

  await setup.locator(".agent-tool-card").filter({ hasText: "Function" }).first().getByRole("button", { name: "Remove" }).click();
  await setup.getByRole("button", { name: "Save changes" }).click();
  await expect(setup.getByRole("status")).toContainText("Agent definition updated");

  requests = await fixtureRequests(request);
  updates = requests.filter((entry) => entry.method === "POST" && entry.path.startsWith("/v1/agents/agent_created_"));
  expect(updates).toHaveLength(2);
  expect(updates[1]?.body?.tools).toEqual([]);
});

test("keeps Source Files controls out of the System status page", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "System", exact: true }).click();
  await expect(page.getByRole("region", { name: "Source Files" })).toHaveCount(0);
  await expect(page.locator(".system-page")).not.toContainText("Source Files");
});

test("supports global Create keyboard navigation and consumes setup requests once", async ({ page, request }) => {
  await openAgents(page, request);
  const sidebar = page.locator(".app-sidebar");
  const productNavigation = sidebar.getByRole("navigation", { name: "Agents product" });
  await expect(productNavigation).toBeVisible();
  await expect(productNavigation.getByRole("button", { name: "Agents", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".product-header").getByRole("navigation", { name: "Agents product" })).toHaveCount(0);
  const createMenu = page.getByRole("button", { name: "Create", exact: true });
  await createMenu.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("menuitem", { name: /^Agent\b/ })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(createMenu).toBeFocused();
  await page.keyboard.press("ArrowDown");
  const createAgentItem = page.getByRole("menuitem", { name: /^Agent\b/ });
  const startSessionItem = page.getByRole("menuitem", { name: /^Start Session\b/ });
  await expect(createAgentItem).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(startSessionItem).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(createAgentItem).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel("Name")).toBeFocused();
  await expect(page.getByRole("heading", { name: "Request preview" })).toBeVisible();

  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "New Agent" })).toHaveCount(0);

  const editTrigger = page.getByRole("button", { name: /^Edit Lifecycle Agent/ });
  await editTrigger.focus();
  await editTrigger.click();
  await expect(page.locator(".agent-setup-page").getByRole("heading", { name: "Lifecycle Agent", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(editTrigger).toBeFocused();
  await expect(page.locator(".modal-backdrop")).toHaveCount(0);

  await createMenu.click();
  await startSessionItem.click();
  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await expect(sessionDialog).toBeVisible();
  await expect(sessionDialog.getByLabel("Saved Agent", { exact: true })).toHaveValue("agent_a");
  await expect(sessionDialog.locator('option[value="agent_a"]')).toBeEnabled();
  await expect(sessionDialog.locator('option[value="agent_a"]')).toContainText("Lifecycle Agent");
  await expect(sessionDialog.locator('option[value="agent_a"]')).not.toContainText("Session requires changes");
  await expect(sessionDialog.locator('option[value="agent_tool_only"]')).toBeEnabled();
  await expect(sessionDialog.locator('option[value="agent_tool_only"]')).toContainText("Saved-only Tool Agent");
  await expect(sessionDialog.locator('option[value="agent_tool_only"]')).not.toContainText("Session requires changes");
  const sessionPostsBeforeCancel = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  )).length;
  await sessionDialog.getByRole("button", { name: "Cancel" }).click();
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(sessionPostsBeforeCancel);
  await expect(createMenu).toBeFocused();
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Create a Session" })).toHaveCount(0);

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await createMenu.click();
  await createAgentItem.click();
  const createMetadata = page.locator(".agent-metadata-input");
  const createName = page.locator('input[data-agent-initial-focus="true"]');
  await expect(createName).toBeFocused();
  await createMetadata.fill('{"owner":"local-test"}');
  await expect(createMetadata).toHaveValue('{"owner":"local-test"}');
  await expect(createName).toHaveValue("");
  await createName.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("status")).toContainText("Agent definition saved as");

  await createMenu.click();
  await createAgentItem.click();
  await expect(page.getByLabel("Name")).toHaveValue("");
  await expect(page.getByLabel("Name")).toBeEnabled();
  await expect(page.getByRole("status")).toHaveCount(0);
  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(createMenu).toBeFocused();

  const catalogCreate = page.getByRole("button", { name: /^Create agent/ });
  await catalogCreate.click();
  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(catalogCreate).toBeFocused();

  const requests = await fixtureRequests(request);
  const creates = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(creates).toHaveLength(1);
  expect(creates[0]?.body).toMatchObject({
    name: null,
    instructions: null,
    metadata: { owner: "local-test" },
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
  });
  expect(creates[0]?.body).not.toHaveProperty("reasoning");
});

test("continues from a default Agent definition into a Session with initial input", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: /^Create agent/ }).click();
  await page.getByLabel("Name").fill("Session-safe Agent");
  await page.getByRole("button", { name: "Save Agent definition" }).click();
  await expect(page.getByRole("status")).toContainText("Agent definition saved as");

  const requestsAfterSave = await fixtureRequests(request);
  const create = requestsAfterSave.find((entry) => entry.method === "POST" && entry.path === "/v1/agents");
  expect(create?.body).toMatchObject({
    name: "Session-safe Agent",
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
  });
  expect(create?.body).not.toHaveProperty("reasoning");

  await expect(page.getByRole("button", { name: "Start Session" })).toBeEnabled();
  await page.getByRole("button", { name: "Start Session" }).click();
  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await sessionDialog.getByRole("textbox", { name: /^First message\b/u }).fill("Review this Agent definition.");
  await expect(sessionDialog).toBeVisible();
  await expect(sessionDialog.getByLabel("Saved Agent", { exact: true })).toHaveValue(/^agent_created_/);
  await expect(sessionDialog.getByRole("button", { name: /Advanced settings/ })).toHaveAttribute("aria-expanded", "false");
  await expect(sessionDialog.getByRole("textbox", { name: /^Additional metadata\b/u })).toHaveCount(0);
  await expect(sessionDialog.getByRole("radio", { name: /No environment/ })).toBeChecked();
  await sessionDialog.getByRole("button", { name: "Create Session" }).click();
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(environmentTrigger(page)).toHaveCount(0);
  await expect(
    page.getByRole("tabpanel", { name: "Conversation" })
      .getByRole("region", { name: "Environment and Workspace status" }),
  ).toHaveCount(0);

  const sessionCreates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(sessionCreates).toHaveLength(1);
  expect(sessionCreates[0]?.body).toMatchObject({
    agent_id: expect.stringMatching(/^agent_created_/),
    environment: { type: "none" },
    stream: true,
  });
});

test("opens repair setup for a saved response while blocking an invalid Session write", async ({ page, request }) => {
  await openAgents(page, request);
  await controlFixture(request, { createAgentResponseVariant: "reasoning" });
  await page.getByRole("button", { name: /^Create agent/ }).click();
  await page.getByLabel("Name").fill("Core-adjusted Agent");
  await page.getByRole("button", { name: "Save Agent definition" }).click();

  await expect(page.getByRole("status")).toContainText("Agent definition saved as");
  await expect(page.locator("#created-agent-session-blocker")).toContainText("Start Session is unavailable");
  const startSession = page.getByRole("button", { name: "Start Session" });
  await expect(startSession).toBeDisabled();

  // Bypass the setup view's disabled control to inspect the explicit repair
  // surface. The dialog remains fail-closed until the saved-only field is reset.
  await startSession.evaluate((button) => {
    const propsKey = Object.getOwnPropertyNames(button).find((key) => key.startsWith("__reactProps$"));
    if (!propsKey) throw new Error("React event props were not found on the Start Session button.");
    const props = (button as unknown as Record<string, { onClick?: () => void }>)[propsKey];
    if (!props?.onClick) throw new Error("Start Session does not have an onClick handler.");
    props.onClick();
  });
  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await expect(sessionDialog).toBeVisible();
  await expect(sessionDialog.getByLabel("Saved Agent", { exact: true })).toHaveValue(/^agent_created_/u);
  await expect(sessionDialog.getByRole("alert")).toContainText("explicit reasoning options are saved-only");
  await expect(sessionDialog.getByRole("button", { name: "Create Session" })).toBeDisabled();
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(0);
});

test("starts only Agents that pass known Session admission", async ({ page, request }) => {
  await openAgents(page, request);
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Edit Lifecycle Agent/ })).toBeVisible();

  const blockedStart = page.getByRole("button", { name: /^Start a Session with Lifecycle Agent/ });
  await expect(blockedStart).toContainText("Unavailable");
  await expect(blockedStart).toHaveAttribute("aria-disabled", "true");
  await blockedStart.focus();
  await expect(blockedStart.locator("xpath=..").getByRole("tooltip")).toBeVisible();
  const blockedSessionCount = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  )).length;
  await page.keyboard.press("Enter");
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(blockedSessionCount);

  const toolOnlyStart = page.getByRole("button", { name: /^Start a Session with Saved-only Tool Agent/ });
  await expect(toolOnlyStart).toHaveAttribute("aria-disabled", "true");
  await toolOnlyStart.focus();
  await expect(toolOnlyStart.locator("xpath=..").getByRole("tooltip")).toContainText("tool_search is saved-only");
  await page.keyboard.press("Enter");
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(blockedSessionCount);
  await expect(page.getByRole("button", { name: /^Start a Session with Second Agent/ })).toBeEnabled();
  await expect(page.getByRole("button", { name: /^Start a Session with Second Agent/ })).toContainText("Start Session");

  const before = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  )).length;
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await sessionDialog.getByRole("textbox", { name: /^First message\b/u }).fill("Check the selected Agent configuration.");
  await expect(sessionDialog).toBeVisible();
  await expect(sessionDialog.getByLabel("Saved Agent", { exact: true })).toHaveValue("agent_b");
  await sessionDialog.getByRole("button", { name: "Create Session" }).click();
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toHaveAttribute("aria-current", "page");

  const sessionCreates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(sessionCreates).toHaveLength(before + 1);
  expect(sessionCreates.at(-1)?.body).toMatchObject({
    agent_id: "agent_b",
    environment: { type: "none" },
    stream: true,
  });
});

test("serializes complete saved-Agent overrides with initial input", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.getByRole("button", { name: "New Session" }).click();
  let dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("Review the Session-specific settings.");
  await dialog.getByLabel("Saved Agent", { exact: true }).selectOption("agent_a");
  await expect(dialog.getByRole("alert")).toContainText("multi-agent execution is not supported");

  await openAdvancedSessionSettings(dialog);
  await dialog.getByRole("checkbox", { name: /Configure Session-only overrides/ }).check();
  await dialog.getByRole("checkbox", { name: "Replace model" }).check();
  await dialog.getByLabel("Session model").fill("fixture/session-model");
  await dialog.getByRole("checkbox", { name: "Replace instructions" }).check();
  await dialog.getByLabel("Session instructions").fill("   ");
  await dialog.getByRole("checkbox", { name: /Replace text configuration/ }).check();
  await dialog.getByLabel("Text verbosity").selectOption("low");
  await dialog.getByRole("checkbox", { name: "Reset multi-agent to disabled" }).check();
  await dialog.getByRole("checkbox", { name: "Reset reasoning to Core defaults" }).check();
  await dialog.getByRole("checkbox", { name: "Reset service tier to auto" }).check();
  await dialog.getByRole("radio", { name: "Clear all" }).check();
  await expect(dialog.getByRole("checkbox", { name: /Stream idle creation events/ })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeEnabled();
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);

  await page.getByRole("button", { name: "New Session" }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("Review the Session-specific settings.");
  await dialog.getByLabel("Saved Agent", { exact: true }).selectOption("agent_b");
  await openAdvancedSessionSettings(dialog);
  await expect(dialog.getByRole("checkbox", { name: /Stream idle creation events/ })).toHaveCount(0);
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);

  const creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates).toHaveLength(2);
  expect(creates[0]?.body).toEqual({
    agent_id: "agent_a",
    agent: {
      model: "fixture/session-model",
      instructions: null,
      multi_agent: null,
      reasoning: null,
      service_tier: null,
      text: { format: { type: "text" }, verbosity: "low" },
      tools: [],
    },
    environment: { type: "none" },
    metadata: {},
    input: "Review the Session-specific settings.",
    stream: true,
    vault_ids: [],
  });
  expect(creates[1]?.body).toMatchObject({
    agent_id: "agent_b",
    environment: { type: "none" },
    metadata: {},
    input: "Review the Session-specific settings.",
    stream: true,
    vault_ids: [],
  });
  expect(creates[1]?.body).not.toHaveProperty("agent");
  expect(creates[1]?.body?.input).toBe("Review the Session-specific settings.");
});

test("derives manual Vault attachments for anonymous and explicit MCP Credentials", async ({ page, request }) => {
  await resetFixture(request);
  const serverURL = "https://mcp.vault-fixture.test/tools";
  const createVault = async (name: string) => {
    const response = await request.post(`${fixtureBaseUrl}/v1/vaults`, { data: { name, metadata: {} } });
    expect(response.ok()).toBe(true);
    return response.json() as Promise<{ id: string }>;
  };
  const createCredential = async (vaultId: string, name: string) => {
    const response = await request.post(`${fixtureBaseUrl}/v1/vaults/${vaultId}/credentials`, { data: {
      name,
      auth: { type: "static_bearer", mcp_server_url: serverURL, token: "fixture-secret-never-rendered" },
    } });
    expect(response.ok()).toBe(true);
    return response.json() as Promise<{ id: string }>;
  };
  const vaultAlpha = await createVault("Vault Alpha");
  const vaultBeta = await createVault("Vault Beta");
  const credentialAlpha = await createCredential(vaultAlpha.id, "Credential Alpha");
  await createCredential(vaultBeta.id, "Credential Beta");
  const mcpTool = (credentialId: string | null) => ({
    type: "mcp",
    server_label: "docs",
    transport: { type: "http", server_url: serverURL, headers: {} },
    allowed_tools: null,
    connection_origin: "service",
    credential_id: credentialId,
    request_metadata: {},
    required: false,
  });
  for (const [name, credentialId] of [
    ["Anonymous MCP Agent", null],
    ["Explicit MCP Agent", credentialAlpha.id],
  ] as const) {
    const response = await request.post(`${fixtureBaseUrl}/v1/agents`, { data: {
      model: "fixture/model-mcp",
      name,
      tools: [mcpTool(credentialId)],
    } });
    expect(response.status()).toBe(201);
  }

  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();

  await page.getByRole("button", { name: /^Start a Session with Anonymous MCP Agent/ }).click();
  let dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("List the available documentation resources.");
  await openAdvancedSessionSettings(dialog);
  await expect(dialog).toContainText("Anonymous for this Session");
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Anonymous MCP Agent/ }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("List the available documentation resources.");
  await openAdvancedSessionSettings(dialog);
  await dialog.getByRole("checkbox", { name: "Vault Alpha" }).check();
  await expect(dialog).toContainText("Implicit unique match · Credential Alpha · Vault Alpha");
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Anonymous MCP Agent/ }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("List the available documentation resources.");
  await openAdvancedSessionSettings(dialog);
  await dialog.getByRole("checkbox", { name: "Vault Alpha" }).check();
  await dialog.getByRole("checkbox", { name: "Vault Beta" }).check();
  await expect(dialog.getByRole("alert")).toContainText("matches multiple Credentials");
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeDisabled();
  await dialog.getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Explicit MCP Agent/ }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("List the available documentation resources.");
  await openAdvancedSessionSettings(dialog);
  const requiredVault = dialog.getByRole("checkbox", { name: /Vault Alpha · attached automatically/ });
  await expect(requiredVault).toBeChecked();
  await expect(requiredVault).toBeDisabled();
  await expect(dialog).toContainText("Explicit · Credential Alpha · Vault Alpha");
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);

  let creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates).toHaveLength(3);
  expect(creates.map((entry) => entry.body?.vault_ids)).toEqual([
    [],
    [vaultAlpha.id],
    [vaultAlpha.id],
  ]);
  const createsBeforeCredentialDelete = creates.length;

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Explicit MCP Agent/ }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("List the available documentation resources.");
  const deleted = await request.delete(`${fixtureBaseUrl}/v1/vaults/${vaultAlpha.id}/credentials/${credentialAlpha.id}`);
  expect(deleted.ok()).toBe(true);
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog.getByRole("alert")).toContainText("explicit MCP Credential is unavailable");
  creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates.length - createsBeforeCredentialDelete).toBeLessThanOrEqual(1);
});

test("clears manual Vault attachments when overrides remove HTTP MCP tools", async ({ page, request }) => {
  await resetFixture(request);
  const serverURL = "https://mcp.vault-clear.test/tools";
  const vaultResponse = await request.post(`${fixtureBaseUrl}/v1/vaults`, {
    data: { name: "Vault to clear", metadata: {} },
  });
  expect(vaultResponse.ok()).toBe(true);
  const vault = await vaultResponse.json() as { id: string };
  const agentResponse = await request.post(`${fixtureBaseUrl}/v1/agents`, { data: {
    model: "fixture/model-mcp-clear",
    name: "MCP Clear Agent",
    tools: [{
      type: "mcp",
      server_label: "docs",
      transport: { type: "http", server_url: serverURL, headers: {} },
      allowed_tools: null,
      connection_origin: "service",
      credential_id: null,
      request_metadata: {},
      required: false,
    }],
  } });
  expect(agentResponse.status()).toBe(201);

  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with MCP Clear Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("Explain the Session without MCP tools.");
  await openAdvancedSessionSettings(dialog);
  await dialog.getByRole("checkbox", { name: "Vault to clear" }).check();
  await dialog.getByRole("checkbox", { name: /Configure Session-only overrides/ }).check();
  await dialog.getByRole("radio", { name: "Clear all" }).check();

  await expect(dialog.getByRole("heading", { name: "Tools & Vaults" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);
  const creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates).toHaveLength(1);
  expect(creates[0]?.body).toMatchObject({
    agent: { tools: [] },
    vault_ids: [],
  });
  expect(creates[0]?.body?.vault_ids).not.toContain(vault.id);
});

test("creates the bounded self-hosted profile and renders a secret-free connection guide", async ({ page, request }) => {
  await openAgents(page, request);
  const sessionPostsBefore = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  )).length;

  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Saved Agent", { exact: true })).toHaveValue("agent_b");
  await dialog.getByRole("radio", { name: /Self-hosted/ }).check();
  const workspace = dialog.getByLabel("Workspace directory");
  await workspace.fill("relative/workspace");
  await expect(dialog.getByText("Workspace directory must be an absolute POSIX path")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeDisabled();
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(sessionPostsBefore);

  await workspace.fill("/executor/workspace");
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeEnabled();
  await dialog.getByRole("button", { name: "Create Session" }).click();

  const { dialog: environmentDialog, panel: environmentPanel, trigger } = await openEnvironmentDialog(page);
  await expect(environmentPanel).toBeVisible();
  await expect(environmentPanel).toContainText("Self-hosted Environment");
  await expect(environmentPanel).toContainText("0f745b0d-b545-49cd-8d7e-4c31c80dc564");
  await expect(environmentPanel).toContainText("https://executor.example.test");
  await expect(environmentPanel).toContainText("/executor/workspace");
  await expect(environmentPanel).toContainText("Pending");
  const files = environmentPanel.getByRole("region", { name: "Workspace files" });
  await expect(files.getByLabel("Directory")).toHaveValue("/executor/workspace");
  await expect(files).toContainText("Workspace root");

  await expect(environmentPanel.locator("details.environment-launcher-guide")).toHaveAttribute("open", "");
  await expect(environmentPanel.getByRole("button", { name: "Copy native command" })).toBeVisible();
  await expect(environmentPanel).toContainText("agents-api-codex-executor");
  await expect(environmentPanel).toContainText("$HOME/.parsar/executor-key.json");
  await expect(environmentPanel).toContainText("Web copies its path but never creates, reads, stores, or transmits the key");
  await expect(environmentPanel).not.toContainText("executor_token");
  await expect(environmentPanel).not.toContainText("Authorization: Bearer");

  await page.keyboard.press("Escape");
  await expect(environmentDialog).toHaveCount(0);
  await expect(trigger).toBeFocused();

  const requests = await fixtureRequests(request);
  const sessionCreates = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions");
  expect(sessionCreates).toHaveLength(sessionPostsBefore + 1);
  expect(sessionCreates.at(-1)?.body).toEqual({
    agent_id: "agent_b",
    environment: {
      type: "self_hosted",
      workspace_directory: "/executor/workspace",
      capability_directories: [],
    },
    metadata: {},
    stream: false,
    vault_ids: [],
  });
  expect(requests.filter((entry) => entry.method === "POST" && entry.path.endsWith("/events"))).toHaveLength(0);
});

test("saves a reusable Environment Template and references it from a managed Session", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();

  const templateSelect = dialog.getByLabel("Reusable Environment Template");
  await expect(templateSelect).toBeDisabled();
  await expect(dialog).toContainText("This Core exposes no saved Templates");

  await dialog.getByRole("button", { name: "New Template" }).click();
  await dialog.getByLabel("Template name").fill("Restricted outbound access");
  await dialog.getByLabel("Template network access").selectOption("disabled");
  await dialog.getByRole("button", { name: "Save Template" }).click();

  await expect(templateSelect).toBeEnabled();
  await expect(templateSelect).toHaveValue(/^4/);
  const selectedTemplateId = await templateSelect.inputValue();
  await expect(dialog.getByLabel("Managed Environment network access"))
    .toContainText("Inherit from Template (disabled)");

  const templateCreates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/environments/templates"
  ));
  expect(templateCreates).toHaveLength(1);
  expect(templateCreates[0]?.beta).toBe("agents=v1");
  expect(templateCreates[0]?.body).toEqual({
    name: "Restricted outbound access",
    network: { access: "disabled" },
  });

  // A Session can keep or narrow the Template policy, never widen it.
  await dialog.getByLabel("Managed Environment network access").selectOption("enabled");
  await expect(dialog).toContainText("never widen it");
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeDisabled();
  await dialog.getByLabel("Managed Environment network access").selectOption("default");

  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");

  const creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  const latest = creates.at(-1)?.body;
  expect(latest?.environment).toEqual({
    type: "openai_hosted",
    environment_template_id: selectedTemplateId,
  });
  expect(latest?.stream).toBe(false);
});

test("hides Template selection when the connected Core lacks the resource", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, { environmentTemplateListStatus: 400 });
  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Agents" }).click();
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();

  await expect(dialog).toContainText("does not expose the Environment Template resource");
  await expect(dialog.getByLabel("Reusable Environment Template")).toBeDisabled();
  await expect(dialog.getByRole("button", { name: "New Template" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeEnabled();
});

test("creates managed hosted profiles with JSON for empty input and SSE for initial input", async ({ page, request }) => {
  await openAgents(page, request);
  const profiles = [
    { label: "default", option: "default", environment: { type: "openai_hosted" }, input: undefined },
    { label: "enabled", option: "enabled", environment: { type: "openai_hosted", network: { access: "enabled" } }, input: "Managed initial input" },
    { label: "disabled", option: "disabled", environment: { type: "openai_hosted", network: { access: "disabled" } }, input: undefined },
  ] as const;

  for (const [index, profile] of profiles.entries()) {
    if (index > 0) await page.getByRole("button", { name: "Agents", exact: true }).click();
    if (index === 0) await controlFixture(request, { environmentEventStatus: 3, environmentEventCount: 1 });
    await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
    const dialog = page.getByRole("dialog", { name: "Create a Session" });
    await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
    const network = dialog.getByLabel("Managed Environment network access");
    await network.selectOption(profile.option);
    if (profile.input) await dialog.getByRole("textbox", { name: /^First message\b/u }).fill(profile.input);
    await dialog.getByRole("button", { name: "Create Session" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
    await expect(connectedLiveEvents(page)).toBeVisible();

    const creates = (await fixtureRequests(request)).filter((entry) => (
      entry.method === "POST" && entry.path === "/v1/agents/sessions"
    ));
    const latest = creates.at(-1)?.body;
    expect(latest).toMatchObject({
      agent_id: "agent_b",
      environment: profile.environment,
      stream: profile.input !== undefined,
      vault_ids: [],
    });
    expect(latest?.environment).toEqual(profile.environment);
    if (profile.input) expect(latest?.input).toBe(profile.input);
    else expect(latest).not.toHaveProperty("input");

    if (index === 0) {
      const { dialog: environmentDialog, panel } = await openEnvironmentDialog(page);
      await expect(panel).toContainText("Managed hosted Environment");
      await expect(panel).toContainText("7a263c51-6bf0-4d53-8518-c792eb1f0d21");
      await expect(panel).toContainText("Network access");
      await expect(panel).toContainText("Enabled");
      await expect(panel).toContainText("/workspace");
      await expect(panel).toContainText("None installed by the basic profile");
      await expect(panel).toContainText("Workspace files");
      await expect(panel.getByText("Add inline Workspace file", { exact: true })).toBeVisible();
      await expect(panel).not.toContainText("Connect Environment");
      await expect(panel).not.toContainText("Remote URL");

      await panel.getByLabel("Local file").setInputFiles({
        name: "managed.txt",
        mimeType: "text/plain",
        buffer: Buffer.from("managed bytes", "utf8"),
      });
      await panel.getByLabel("Destination path").fill("/workspace/managed.txt");
      await panel.getByRole("button", { name: "Write selected file" }).click();
      await expect(panel).toContainText("Write confirmed:");
      await expect(panel).toContainText("/workspace/managed.txt");
      let writes = (await fixtureRequests(request)).filter((entry) => (
        entry.method === "POST" && entry.path.endsWith("/environments/7a263c51-6bf0-4d53-8518-c792eb1f0d21/files")
      ));
      expect(writes.at(-1)?.body).toEqual({
        type: "inline",
        data: Buffer.from("managed bytes", "utf8").toString("base64"),
        path: "/workspace/managed.txt",
      });

      await panel.getByLabel("Local file").setInputFiles({
        name: "unknown.txt",
        mimeType: "text/plain",
        buffer: Buffer.from("uncertain bytes", "utf8"),
      });
      await panel.getByLabel("Destination path").fill("/workspace/unknown.txt");
      await controlFixture(request, { environmentFileCreateResponseLoss: 1 });
      const writesBeforeUnknown = writes.length;
      await panel.getByRole("button", { name: "Write selected file" }).click();
      await expect(panel.getByRole("alert")).toContainText("Web did not retry the request");
      writes = (await fixtureRequests(request)).filter((entry) => (
        entry.method === "POST" && entry.path.endsWith("/environments/7a263c51-6bf0-4d53-8518-c792eb1f0d21/files")
      ));
      expect(writes).toHaveLength(writesBeforeUnknown + 1);
      await page.waitForTimeout(250);
      expect((await fixtureRequests(request)).filter((entry) => (
        entry.method === "POST" && entry.path.endsWith("/environments/7a263c51-6bf0-4d53-8518-c792eb1f0d21/files")
      ))).toHaveLength(writesBeforeUnknown + 1);
      await environmentDialog.getByRole("button", { name: "Done" }).click();
    }
  }

  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  await expect(page.getByRole("table", { name: "Recent Sessions" })).toContainText("Managed hosted");
});

test("blocks hosted MCP before persistence while allowing a Function-only managed Session", async ({ page, request }) => {
  await resetFixture(request);
  const mcp = await request.post(`${fixtureBaseUrl}/v1/agents`, { data: {
    model: "fixture/model-mcp",
    name: "Hosted MCP Agent",
    tools: [{
      type: "mcp",
      server_label: "docs",
      transport: { type: "http", server_url: "https://mcp.example/tools", headers: {} },
      allowed_tools: null,
      connection_origin: "service",
      credential_id: null,
      request_metadata: {},
      required: false,
    }],
  } });
  expect(mcp.status()).toBe(201);
  const fn = await request.post(`${fixtureBaseUrl}/v1/agents`, { data: {
    model: "fixture/model-function",
    name: "Hosted Function Agent",
    tools: [{ type: "function", name: "lookup", description: "", parameters: {}, defer_loading: false }],
  } });
  expect(fn.status()).toBe(201);

  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  const before = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  )).length;
  await page.getByRole("button", { name: /^Start a Session with Hosted MCP Agent/ }).click();
  let dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
  await expect(dialog.getByRole("alert")).toContainText("Managed hosted Sessions do not yet support MCP tools");
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeDisabled();
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ))).toHaveLength(before);
  await dialog.getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Hosted Function Agent/ }).click();
  dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
  await expect(dialog.getByRole("button", { name: "Create Session" })).toBeEnabled();
  await dialog.getByRole("button", { name: "Create Session" }).click();
  await expect(dialog).toHaveCount(0);
  const creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates).toHaveLength(before + 1);
  expect(creates.at(-1)?.body).toMatchObject({ environment: { type: "openai_hosted" }, stream: false });
});

test("keeps managed Environment resource and terminal event states fail-closed", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, { environmentScenario: 8, environmentResourceStatus: "expired" });
  await openSessionsFromHome(page);
  let trigger = environmentTrigger(page);
  await expect(trigger).toContainText("Managed Environment expired");
  let opened = await openEnvironmentDialog(page);
  await expect(opened.panel).toContainText("Managed Environment expired");
  await expect(opened.panel.getByText("Add inline Workspace file", { exact: true })).toHaveCount(0);
  await expect(opened.panel).not.toContainText("Connect Environment");
  await opened.dialog.getByRole("button", { name: "Done" }).click();

  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  await expect(page.getByRole("table", { name: "Recent Sessions" })).toContainText("Managed hosted");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await controlFixture(request, { environmentResourceStatus: "pending", environmentResourceVariant: "wrong_type" });
  await page.getByRole("button", { name: "Recover durable state" }).click();
  trigger = environmentTrigger(page);
  await expect(trigger).toContainText("Managed Environment unavailable");
  opened = await openEnvironmentDialog(page);
  await expect(opened.panel).toContainText("Durable managed Environment status is unavailable");
  await expect(opened.panel.getByText("Add inline Workspace file", { exact: true })).toHaveCount(0);
  await opened.dialog.getByRole("button", { name: "Done" }).click();

  await controlFixture(request, {
    environmentResourceVariant: "valid",
    environmentEventStatus: 5,
    environmentEventCount: 1,
  });
  await page.reload();
  await expect(environmentTrigger(page)).toContainText("Managed Environment failed");
  opened = await openEnvironmentDialog(page);
  await expect(opened.panel).toContainText("Managed Environment failed");
  await expect(opened.panel.getByText("Add inline Workspace file", { exact: true })).toHaveCount(0);
  await expect(opened.panel).not.toContainText("Connect Environment");

  await opened.dialog.getByRole("button", { name: "Done" }).click();
  await controlFixture(request, {
    environmentScenario: 9,
    environmentResourceStatus: "failed",
    environmentResourceVariant: "valid",
    environmentEventCount: 0,
  });
  await page.reload();
  await expect(page.getByText("Session cannot continue", { exact: true })).toBeVisible();
  await expect(page.getByText("The environment is no longer available for this input.", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Message the Agent")).toBeDisabled();
  opened = await openEnvironmentDialog(page);
  await expect(opened.panel).toContainText("Managed Environment failed");
  await expect(opened.panel.getByText("Add inline Workspace file", { exact: true })).toHaveCount(0);
  await expect(opened.panel).not.toContainText("Connect Environment");

  await opened.dialog.getByRole("button", { name: "Done" }).click();
  await controlFixture(request, {
    environmentScenario: 8,
    environmentResourceStatus: "pending",
    environmentResourceVariant: "populated",
  });
  await page.reload();
  opened = await openEnvironmentDialog(page);
  await expect(opened.panel.getByText("Add inline Workspace file", { exact: true })).toHaveCount(0);
  await expect(opened.panel).not.toContainText("Connect Environment");
});

test("creates an inline Session without saved Agents and preserves ordered user-message input", async ({ page, request }) => {
  await openAgents(page, request);
  for (const agentId of ["agent_a", "agent_b", "agent_tool_only"]) {
    const deleted = await request.delete(`${fixtureBaseUrl}/v1/agents/${agentId}`);
    expect(deleted.ok()).toBe(true);
  }
  await page.getByRole("button", { name: "Refresh Agents" }).click();
  await expect(page.getByText("No saved Agents", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  const newSession = page.getByRole("button", { name: "New Session" });
  await expect(newSession).toBeEnabled();
  await newSession.click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await openAdvancedSessionSettings(dialog);
  await expect(dialog.getByRole("radio", { name: /Inline Agent/ })).toBeChecked();
  await expect(dialog.getByRole("radio", { name: /Saved Agent/ })).toBeDisabled();
  await dialog.getByLabel("Inline model").fill("fixture/inline-model");
  await dialog.getByLabel("Inline instructions").fill("Keep exact input structure.");
  const tools = dialog.getByRole("region", { name: "Inline tools" });
  await tools.getByRole("button", { name: "Add Function" }).click();
  await tools.getByLabel("Name", { exact: true }).fill("lookup_fixture");
  await tools.getByLabel("Description", { exact: true }).fill("Look up fixture data");
  await tools.getByRole("textbox", { name: /^Parameters JSON Schema/ }).fill('{"type":"object","properties":{"id":{"type":"string"}}}');

  await dialog.getByRole("radio", { name: "Message array" }).check();
  await expect(dialog.getByRole("alert").filter({ hasText: "User message 1 needs nonblank text across its parts." })).toHaveCount(1);
  const advancedToggle = dialog.getByRole("button", { name: /Advanced settings/ });
  await advancedToggle.click();
  await expect(dialog.getByRole("button", { name: "Edit messages", exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Edit messages", exact: true }).click();
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "true");
  let messages = dialog.locator(".session-initial-message");
  await messages.nth(0).locator(".session-initial-part textarea").nth(0).fill("first-message-part-one");
  await dialog.getByRole("button", { name: "Add text part" }).click();
  await messages.nth(0).locator(".session-initial-part textarea").nth(1).fill("first-message-part-two");
  await dialog.getByRole("button", { name: "Add message" }).click();
  messages = dialog.locator(".session-initial-message");
  await messages.nth(1).locator(".session-initial-part textarea").fill("second-message");
  await dialog.getByRole("button", { name: "Move user message 2 up" }).click();
  await dialog.getByRole("button", { name: "Move text part 2 of user message 2 up" }).click();
  await expect(dialog).not.toContainText("Creation events stream automatically");
  await expect(dialog.getByRole("checkbox", { name: /Stream idle creation events/ })).toHaveCount(0);
  await dialog.getByRole("button", { name: "Create Session" }).click();

  await expect(dialog).toHaveCount(0);
  const conversation = page.getByRole("tabpanel", { name: "Conversation" });
  await expect(conversation.getByText("second-message", { exact: true })).toBeVisible();
  await expect(conversation).toContainText("first-message-part-two");
  await expect(conversation).toContainText("first-message-part-one");
  const creates = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(creates).toHaveLength(1);
  expect(creates[0]?.body).toEqual({
    agent: {
      model: "fixture/inline-model",
      instructions: "Keep exact input structure.",
      tools: [{
        type: "function",
        name: "lookup_fixture",
        description: "Look up fixture data",
        parameters: { type: "object", properties: { id: { type: "string" } } },
        defer_loading: false,
      }],
    },
    environment: { type: "none" },
    input: [
      { type: "message", role: "user", content: [{ type: "input_text", text: "second-message" }] },
      {
        type: "message",
        role: "user",
        content: [
          { type: "input_text", text: "first-message-part-two" },
          { type: "input_text", text: "first-message-part-one" },
        ],
      },
    ],
    metadata: {},
    stream: true,
    vault_ids: [],
  });
  expect(creates[0]?.body).not.toHaveProperty("agent_id");
});

test("keeps failures visible, rejects stale async continuations, and never retries writes", async ({ page, request }) => {
  await openAgents(page, request);

  await controlFixture(request, { retrieveStatus: 500 });
  const failedEditTrigger = page.getByRole("button", { name: /^Edit Lifecycle Agent/ });
  await failedEditTrigger.click();
  const openError = page.getByRole("alert");
  await expect(openError).toContainText("Fixture retrieve failed.");
  await expect(failedEditTrigger).toBeEnabled();
  await expect(failedEditTrigger).toBeFocused();
  await openError.getByRole("button", { name: "Retry" }).click();
  await expect(page.locator(".agent-setup-page").getByRole("heading", { name: "Lifecycle Agent", exact: true })).toBeVisible();
  await expect(page.locator(".agent-setup-page")).not.toContainText("stale list");
  await page.getByRole("button", { name: "Back to Agents" }).click();

  await controlFixture(request, { retrieveDelayMs: 400 });
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  await expect(page.getByRole("status")).toHaveText("Opening latest definition…");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.waitForTimeout(500);
  await expect(page.locator(".session-page")).toBeVisible();
  await expect(page.locator(".agent-setup-page")).toHaveCount(0);

  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  await expect(page.locator(".agent-setup-page")).toBeVisible();
  await page.getByLabel("Name").fill("Preserved after failure");
  await controlFixture(request, { updateStatus: 500 });
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert")).toContainText("Fixture update failed.");
  await expect(page.getByLabel("Name")).toHaveValue("Preserved after failure");

  let requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/agent_a")).toHaveLength(1);

  await controlFixture(request, { updateDelayMs: 600 });
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("button", { name: "Back to Agents" })).toBeDisabled();
  await page.waitForTimeout(100);
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.waitForTimeout(700);
  await expect(page.locator(".session-page")).toBeVisible();
  await expect(page.locator(".agent-setup-page")).toHaveCount(0);

  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/agent_a")).toHaveLength(2);
});

test("requires delete confirmation, preserves failures, and keeps Session snapshots", async ({ page, request }) => {
  await openAgents(page, request);
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  await page.getByRole("button", { name: "Delete Agent" }).click();
  let dialog = page.getByRole("dialog", { name: "Delete Agent?" });
  await expect(dialog).toContainText("Existing Sessions keep their durable Agent snapshots");
  await expect(dialog).toContainText("agent_a");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("button", { name: "Delete Agent" })).toBeFocused();

  let requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE" && entry.path === "/v1/agents/agent_a")).toHaveLength(0);

  await page.getByRole("button", { name: "Delete Agent" }).click();
  dialog = page.getByRole("dialog", { name: "Delete Agent?" });
  await controlFixture(request, { deleteStatus: 500 });
  await dialog.getByRole("button", { name: "Delete Agent" }).click();
  await expect(dialog.getByRole("alert")).toContainText("Fixture delete failed.");
  await expect(dialog).toContainText("Lifecycle Agent");

  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE" && entry.path === "/v1/agents/agent_a")).toHaveLength(1);

  await controlFixture(request, { deleteDelayMs: 300 });
  await dialog.getByRole("button", { name: "Delete Agent" }).click();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Edit Lifecycle Agent/ })).toHaveCount(0);

  await page.getByRole("button", { name: "Sessions" }).click();
  await expect(page.getByRole("button", { name: /idle Lifecycle Agent/ })).toBeVisible();
  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE" && entry.path === "/v1/agents/agent_a")).toHaveLength(2);
});

test("keeps the Agent card grid, setup, and delete confirmation usable at 390 px", async ({ page, request }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAgents(page, request);

  const agentList = page.getByRole("list", { name: "Agents", exact: true });
  await expect(agentList.getByRole("listitem")).toHaveCount(2);
  await expect(agentList.getByRole("listitem").first()).toContainText("Create agent");
  await expect(page.getByRole("button", { name: /^Edit Lifecycle Agent/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Edit Second Agent/ })).toHaveCount(0);
  const showMore = page.getByRole("button", { name: "Show 2 more" });
  await expect(showMore).toHaveAttribute("aria-expanded", "false");
  await showMore.click();
  await expect(page.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
  await expect(agentList.getByRole("listitem")).toHaveCount(4);
  const secondAgent = page.getByRole("button", { name: /^Edit Second Agent/ });
  await expect(secondAgent).toBeVisible();
  await secondAgent.click();
  await expect(page.locator(".agent-setup-page").getByRole("heading", { name: "Second Agent", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(secondAgent).toBeFocused();
  await expect(page.getByRole("button", { name: "Show less" })).toBeVisible();

  const metrics = await page.evaluate(() => {
    const main = document.querySelector(".app-main")?.getBoundingClientRect();
    const trigger = Array.from(document.querySelectorAll("button")).find((button) => button.textContent?.includes("Create agent"))?.getBoundingClientRect();
    const grid = document.querySelector(".agent-card-grid")?.getBoundingClientRect();
    const sessionTrigger = document.querySelector('button[aria-label^="Start a Session with Second Agent ("]')?.getBoundingClientRect();
    return {
      innerWidth,
      documentScrollWidth: document.documentElement.scrollWidth,
      bodyScrollWidth: document.body.scrollWidth,
      main: main && { left: main.left, right: main.right, width: main.width },
      trigger: trigger && { left: trigger.left, right: trigger.right, width: trigger.width },
      grid: grid && { left: grid.left, right: grid.right, width: grid.width },
      sessionTrigger: sessionTrigger && { left: sessionTrigger.left, right: sessionTrigger.right, width: sessionTrigger.width },
    };
  });
  expect(metrics.documentScrollWidth).toBeLessThanOrEqual(metrics.innerWidth);
  expect(metrics.bodyScrollWidth).toBeLessThanOrEqual(metrics.innerWidth);
  expect(metrics.main?.left).toBeGreaterThanOrEqual(0);
  expect(metrics.main?.right).toBeLessThanOrEqual(390);
  expect(metrics.trigger?.left).toBeGreaterThanOrEqual(0);
  expect(metrics.trigger?.right).toBeLessThanOrEqual(390);
  expect(metrics.grid?.left).toBeGreaterThanOrEqual(0);
  expect(metrics.grid?.right).toBeLessThanOrEqual(390);
  expect(metrics.sessionTrigger?.left).toBeGreaterThanOrEqual(metrics.grid?.left ?? 0);
  expect(metrics.sessionTrigger?.right).toBeLessThanOrEqual(metrics.grid?.right ?? 390);
  await expect(page.getByRole("button", { name: /^Start a Session with Second Agent/ })).toContainText("Start Session");
  await attachScreenshot(page, testInfo, "narrow-light-agent-card-grid");

  await expect(page.getByRole("button", { name: "Environments", exact: true })).toHaveCount(0);
  const sessionsNavigation = page.getByRole("button", { name: "Sessions", exact: true });
  await sessionsNavigation.click();
  await expect(sessionsNavigation).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".session-page")).toBeVisible();
  await page.getByRole("button", { name: "Agents", exact: true }).click();

  const globalCreate = page.getByRole("button", { name: "Create", exact: true });
  await globalCreate.click();
  const createPanel = page.getByRole("menu", { name: "Create" });
  await expect(createPanel).toBeVisible();
  await expect(createPanel.getByRole("menuitem")).toHaveCount(2);
  await expect(createPanel.getByRole("menuitem", { name: /Environment template/i })).toHaveCount(0);
  await expect(createPanel.getByRole("menuitem", { name: /Environment key/i })).toHaveCount(0);
  const createPanelBox = await createPanel.boundingBox();
  expect(createPanelBox).not.toBeNull();
  expect(createPanelBox?.x ?? -1).toBeGreaterThanOrEqual(0);
  expect((createPanelBox?.x ?? 0) + (createPanelBox?.width ?? 0)).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await createPanel.getByRole("menuitem", { name: /^Agent\b/ }).click();
  const setup = page.locator(".agent-setup-page");
  const box = await setup.boundingBox();
  expect(box).not.toBeNull();
  expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
  expect(box?.y ?? -1).toBeGreaterThanOrEqual(0);
  expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(390.5);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await expect(page.getByRole("button", { name: "Save Agent definition" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Request preview" })).toBeVisible();
  await expect(page.getByLabel("Text format")).toHaveValue("Text");
  await expect(page.getByLabel("Reasoning effort")).toBeDisabled();
  await expect(setup).toContainText("Creation is locked to the current Session-compatible profile");
  await attachScreenshot(page, testInfo, "narrow-light-agent-setup");
  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(globalCreate).toBeFocused();

  await page.getByRole("button", { name: "Dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  const editSetup = page.locator(".agent-setup-page");
  await expect(editSetup).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const editLayout = await editSetup.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      documentScrollWidth: document.documentElement.scrollWidth,
      bodyScrollWidth: document.body.scrollWidth,
      left: box.left,
      right: box.right,
    };
  });
  expect(editLayout.documentScrollWidth).toBeLessThanOrEqual(390);
  expect(editLayout.bodyScrollWidth).toBeLessThanOrEqual(390);
  expect(editLayout.left).toBeGreaterThanOrEqual(0);
  expect(editLayout.right).toBeLessThanOrEqual(390.5);
  await attachScreenshot(page, testInfo, "narrow-dark-agent-edit-setup");

  const deleteAction = editSetup.getByRole("button", { name: "Delete Agent" });
  await deleteAction.scrollIntoViewIfNeeded();
  await expect(deleteAction).toBeInViewport();
  await deleteAction.click();
  const deleteDialog = page.getByRole("dialog", { name: "Delete Agent?" });
  await expect(deleteDialog).toBeVisible();
  await expect(deleteDialog).toContainText("agent_a");
  await expect(deleteDialog.getByRole("button", { name: "Cancel" })).toBeInViewport();
  await expect(deleteDialog.getByRole("button", { name: "Delete Agent" })).toBeInViewport();
  const dialogBox = await deleteDialog.boundingBox();
  expect(dialogBox).not.toBeNull();
  expect(dialogBox?.x ?? -1).toBeGreaterThanOrEqual(0);
  expect((dialogBox?.x ?? 0) + (dialogBox?.width ?? 0)).toBeLessThanOrEqual(390.5);
  await attachScreenshot(page, testInfo, "narrow-dark-agent-delete-confirmation");
  await deleteDialog.getByRole("button", { name: "Cancel" }).click();
  await expect(editSetup).toBeVisible();
  await expect(editSetup.getByRole("button", { name: "Delete Agent" })).toBeFocused();
});

test("requires a first message without an Environment and preserves the draft across environment changes", async ({ page, request }) => {
  await openAgents(page, request);
  const before = await fixtureState(request);
  for (const input of [undefined, null]) {
    for (const fields of [{ environment: { type: "none" } }, { environment: { type: "openai_hosted" }, stream: true }]) {
      const response = await request.post(`${fixtureBaseUrl}/v1/agents/sessions`, { data: {
        agent_id: "agent_b", ...fields, ...(input === undefined ? {} : { input }),
      } });
      expect(response.status()).toBe(400);
    }
  }
  expect((await fixtureState(request)).sessions).toEqual(before.sessions);
  const postsBefore = (await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions").length;
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  const firstMessage = dialog.getByRole("textbox", { name: /^First message\b/u });
  const create = dialog.getByRole("button", { name: "Create Session" });
  await expect(firstMessage).toHaveAttribute("aria-required", "true");
  await expect(create).toBeDisabled();
  await firstMessage.fill(" \t\u0085 ");
  await expect(create).toBeDisabled();
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions")).toHaveLength(postsBefore);
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
  await expect(create).toBeEnabled();
  await expect(firstMessage).toHaveAttribute("aria-required", "false");
  const input = "  Explain this Agent's capabilities.\n";
  await firstMessage.fill(input);
  await dialog.getByRole("radio", { name: /No environment/ }).check();
  await expect(firstMessage).toHaveValue(input);
  await create.click();
  await expect(dialog).toHaveCount(0);
  const creates = (await fixtureRequests(request)).filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions");
  expect(creates).toHaveLength(postsBefore + 1);
  expect(creates.at(-1)?.body).toMatchObject({ environment: { type: "none" }, input, stream: true });
});

test("starts one Session with an idempotency key and without browser authorization", async ({ page, request }) => {
  await openAgents(page, request);
  await startSessionWithSecondAgent(page);
  await expect(page.getByRole("heading", { name: "Sessions" })).toBeVisible();

  const requests = await fixtureRequests(request);
  const creates = requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions");
  expect(creates).toHaveLength(1);
  expect(creates[0]?.idempotencyKeyPresent).toBe(true);
  expect(creates[0]?.body).toMatchObject({ agent_id: "agent_b", environment: { type: "none" }, input: "Review the selected Agent configuration.", stream: true });
  for (const entry of requests.filter((candidate) => candidate.path.startsWith("/v1/"))) {
    expect(entry.beta).toBe("agents=v1");
    expect(entry.authorizationPresent).toBe(false);
  }
});

test("filters every Session page by Agent and aborts a stale filter read", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, { sessionListPageSize: 1 });

  await openSessionsFromHome(page);
  const filter = page.getByLabel("Filter Sessions by Agent");
  await expect(filter).toBeVisible();
  await expect(page.locator(".session-row")).toHaveCount(1);

  // These Sessions are deliberately created after the global snapshot loads.
  // The Agent-scoped list must be able to select them without inflating Dashboard totals.
  await createFixtureSession(request, "first");
  await createFixtureSession(request, "second");

  await filter.selectOption("agent_b");
  await expect(page.locator(".session-row")).toHaveCount(2);
  await expect(page.locator(".session-row").filter({ hasText: "Second Agent" })).toHaveCount(2);
  await expect(page.locator(".session-row").filter({ hasText: "Lifecycle Agent" })).toHaveCount(0);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");

  const filteredReads = (await fixtureRequests(request)).filter((entry) => {
    if (entry.method !== "GET" || entry.path !== "/v1/agents/sessions") return false;
    return new URLSearchParams(entry.query ?? "").get("agent_id") === "agent_b";
  });
  expect(filteredReads).toHaveLength(2);
  expect(filteredReads.every((entry) => (
    new URLSearchParams(entry.query ?? "").get("agent_id") === "agent_b"
  ))).toBe(true);
  expect(filteredReads.some((entry) => (
    new URLSearchParams(entry.query ?? "").has("after")
  ))).toBe(true);

  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  const dashboard = page.locator(".dashboard-page");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Sessions" })).toContainText("1");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await expect(filter).toHaveValue("agent_b");
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");

  const before = await fixtureState(request);
  const agentAReadsBefore = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" &&
    entry.path === "/v1/agents/sessions" &&
    new URLSearchParams(entry.query ?? "").get("agent_id") === "agent_a"
  )).length;
  await controlFixture(request, { sessionListDelayMs: 500 });
  await filter.selectOption("agent_a");
  await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" &&
    entry.path === "/v1/agents/sessions" &&
    new URLSearchParams(entry.query ?? "").get("agent_id") === "agent_a"
  )).length).toBeGreaterThan(agentAReadsBefore);

  await filter.selectOption("agent_b");
  await expect.poll(async () => (await fixtureState(request)).aborts.sessionListReads)
    .toBeGreaterThan(before.aborts.sessionListReads);
  await expect(page.locator(".session-row")).toHaveCount(2);
  await expect(page.locator(".session-row").filter({ hasText: "Second Agent" })).toHaveCount(2);
  await expect(page.locator(".session-row").filter({ hasText: "Lifecycle Agent" })).toHaveCount(0);
  await page.waitForTimeout(550);
  await expect(filter).toHaveValue("agent_b");
  await expect(page.locator(".session-row").filter({ hasText: "Second Agent" })).toHaveCount(2);
  await expect(page.locator(".session-row").filter({ hasText: "Lifecycle Agent" })).toHaveCount(0);

  await filter.selectOption("");
  await expect(filter).toHaveValue("");
  await expect(page.locator(".session-row")).toHaveCount(1);
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");

  await filter.selectOption("agent_b");
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  const lifecycleRow = page.getByRole("table", { name: "Recent Sessions" })
    .getByRole("row")
    .filter({ hasText: "Lifecycle Agent" });
  await lifecycleRow.getByRole("button").click();
  await expect(filter).toHaveValue("");
  await expect(page.locator(".session-row")).toHaveCount(1);
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
});

test("fences the filtered workspace across loading, errors, unavailable Agents, and deletes", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
  await createFixtureSession(request, "delete-first");
  await createFixtureSession(request, "delete-second");

  const filter = page.getByLabel("Filter Sessions by Agent");
  await controlFixture(request, { sessionListDelayMs: 500 });
  await filter.selectOption("agent_b");
  await expect(page.getByLabel("Loading Session workspace")).toBeVisible();
  await expect(page.locator(".conversation-panel")).toHaveCount(0);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");

  await controlFixture(request, { sessionListStatus: 503 });
  await filter.selectOption("agent_a");
  await expect(page.locator(".workspace-error")).toContainText("Couldn’t load Sessions");
  await expect(page.locator(".conversation-panel")).toHaveCount(0);
  await page.locator(".workspace-error").getByRole("button", { name: "Retry" }).click();
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");

  await filter.selectOption("agent_b");
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  const deletedAgent = await request.delete(`${fixtureBaseUrl}/v1/agents/agent_b`);
  expect(deletedAgent.ok()).toBe(true);
  await page.getByRole("button", { name: "Recover durable state" }).click();
  await expect(filter).toHaveValue("agent_b");
  await expect(filter.locator("option:checked")).toHaveText("Unavailable Agent (not loaded)");
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");

  for (const remainingRows of [1, 0]) {
    await page.locator(".conversation-session-action").click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "Delete", exact: true }).click();
    await dialog.getByRole("button", { name: "Delete Session" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator(".session-row")).toHaveCount(remainingRows);
    await expect(page.locator(".session-row").filter({ hasText: "Lifecycle Agent" })).toHaveCount(0);
    if (remainingRows) {
      await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
    }
  }
  await expect(page.locator(".conversation-panel")).toHaveCount(0);
  await expect(page.locator(".workspace-empty")).toContainText("Select or create a Session");
});

test("streams initial Session creation, captures early events, then hands off to one GET stream", async ({ page, request }) => {
  await openAgents(page, request);
  await controlFixture(request, { sessionCreateStreamCloseDelayMs: 1_500 });
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByRole("textbox", { name: /^Title\b/u }).fill("Streamed creation");
  const exactInput = "  Initial streamed input  \n";
  await dialog.getByRole("textbox", { name: /^First message\b/u }).fill(exactInput);
  await dialog.getByRole("button", { name: "Create Session" }).click();

  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".conversation-header h2")).toHaveText("Streamed creation");
  await expect(connectedLiveEvents(page)).toBeVisible();
  await expect(
    page.getByRole("tabpanel", { name: "Conversation" })
      .getByText("Initial streamed input", { exact: true }),
  ).toBeVisible();

  await expect.poll(async () => (await fixtureState(request)).sessions[0]?.id).toMatch(/^session_created_/u);
  const createdSessionId = (await fixtureState(request)).sessions[0]?.id;
  if (!createdSessionId) throw new Error("Fixture did not retain the streamed Session.");
  const createRequests = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions"
  ));
  expect(createRequests).toHaveLength(1);
  expect(createRequests[0]?.body).toMatchObject({
    agent_id: "agent_b",
    environment: { type: "none" },
    input: exactInput,
    metadata: { title: "Streamed creation" },
    stream: true,
    vault_ids: [],
  });

  await page.waitForTimeout(250);
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${createdSessionId}/events`
  ))).toHaveLength(0);

  await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${createdSessionId}/events`
  )).length).toBe(1);
  const handoffRequests = await fixtureRequests(request);
  const getStreamIndex = handoffRequests.findIndex((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${createdSessionId}/events`
  ));
  expect(getStreamIndex).toBeGreaterThan(-1);
  expect(handoffRequests.slice(0, getStreamIndex).filter((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${createdSessionId}`
  )).length).toBeGreaterThanOrEqual(2);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await expect(
    page.getByRole("tabpanel", { name: "Conversation" })
      .getByText("Initial streamed input", { exact: true }),
  ).toBeVisible();
});

for (const failure of [
  { label: "response loss", control: { sessionCreateResponseLoss: 1 }, message: "Agent core request failed (502)." },
  { label: "creation-stream EOF before identity", control: { sessionCreateStreamMissingIdentity: 1 }, message: "Agent core returned an empty event stream." },
]) {
  test(`keeps one Session create attempt across ${failure.label} and an unchanged manual retry`, async ({ page, request }) => {
    await openAgents(page, request);
    await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
    const dialog = page.getByRole("dialog", { name: "Create a Session" });
    await dialog.getByRole("textbox", { name: /^First message\b/u }).fill("Review this request once after response recovery.");
    const create = dialog.getByRole("button", { name: "Create Session" });
    await controlFixture(request, { sessionCreateDelayMs: 1_500, ...failure.control });

    await create.evaluate((button) => {
      button.click();
      button.click();
    });
    await expect(dialog.getByRole("alert")).toContainText(failure.message);
    await expect(dialog.getByText("Retrying this unchanged request reuses the original idempotency key.")).toBeVisible();

    let creates = (await fixtureRequests(request)).filter((entry) => (
      entry.method === "POST" && entry.path === "/v1/agents/sessions"
    ));
    expect(creates).toHaveLength(1);
    const originalKey = creates[0]?.idempotencyKey;
    expect(originalKey).toBeTruthy();
    expect((await fixtureState(request)).sessions).toHaveLength(2);

    await create.click();
    await expect(page.getByRole("heading", { name: "Sessions" })).toBeVisible();
    creates = (await fixtureRequests(request)).filter((entry) => (
      entry.method === "POST" && entry.path === "/v1/agents/sessions"
    ));
    expect(creates).toHaveLength(2);
    expect(creates[1]?.idempotencyKey).toBe(originalKey);
    expect(creates.map((entry) => entry.body?.stream)).toEqual([true, false]);
    expect(creates[1]?.body?.input).toBe(creates[0]?.body?.input);
    const recovered = (await fixtureState(request)).sessions;
    expect(recovered).toHaveLength(2);
    const sessionId = recovered.find((session) => session.id !== "session_snapshot")?.id;
    const turns = await request.get(`${fixtureBaseUrl}/v1/agents/sessions/${sessionId}/turns`);
    const items = await request.get(`${fixtureBaseUrl}/v1/agents/sessions/${sessionId}/items`);
    expect((await turns.json() as { data: unknown[] }).data).toHaveLength(1);
    expect((await items.json() as { data: unknown[] }).data).toHaveLength(1);
  });
}

test("shows composer activity only for a Core-reported in-progress Session", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  const activity = page.locator(".conversation-activity");
  await expect(activity).toHaveCount(0);
  await emitSessionFixture(request, "in_progress");
  await expect(activity).toBeVisible();
  await expect(activity).toContainText("Lifecycle Agent is working…");
  await expect(activity).toHaveAttribute("aria-live", "polite");
  await expect(activity).toHaveAttribute("aria-atomic", "true");
  await expect(activity.locator(".status-running")).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel active Turn" })).toBeVisible();
  await attachElementScreenshot(activity, testInfo, "conversation-activity");

  await emitSessionFixture(request, "idle");
  await expect(activity).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send message" })).toBeVisible();
});

test("shows immediate local feedback while a message submission is waiting for Core", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  let releaseSend: (() => void) | undefined;
  const sendGate = new Promise<void>((resolve) => {
    releaseSend = resolve;
  });
  let interceptedSends = 0;
  await page.route("**/v1/agents/sessions/*/events", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    interceptedSends += 1;
    await sendGate;
    await route.continue();
  });

  const composer = page.getByLabel("Message the Agent");
  await composer.fill("Immediate pending message");
  await composer.press("Enter");

  const pending = page.locator('[data-send-state="sending"]');
  await expect(pending).toBeVisible();
  await expect(pending).toContainText("Immediate pending message");
  await expect(pending).toContainText("Sending…");
  await expect(page.locator(".conversation-activity")).toContainText("Sending message…");
  await expect(page.getByText("Lifecycle Agent is working…")).toHaveCount(0);
  await expect(composer).toHaveValue("");
  await expect.poll(() => interceptedSends).toBe(1);

  releaseSend?.();
  await expect(pending).toHaveCount(0);
  await expect(page.locator(".conversation-activity")).toHaveCount(0);
  await expect.poll(async () => (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length).toBe(1);
});

test("updates Session title and metadata after a latest read while preserving failed and unknown drafts", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  const manage = page.locator(".conversation-session-action");
  const streamReadsBefore = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path.endsWith("/events")
  )).length;

  await manage.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Edit", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Session title", { exact: true }).fill("Renamed Session");
  await dialog.getByLabel("Additional Session metadata", { exact: true }).fill('{"team":"web","note":"safe"}');
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("heading", { name: "Renamed Session" })).toBeVisible();
  await expect(page.locator(".conversation-header h2")).toHaveText("Renamed Session");

  let requests = await fixtureRequests(request);
  const updateIndex = requests.findLastIndex((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  ));
  const latestReadIndex = requests.findLastIndex((entry, index) => (
    index < updateIndex && entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  ));
  expect(latestReadIndex).toBeGreaterThanOrEqual(0);
  expect(latestReadIndex).toBeLessThan(updateIndex);
  expect(requests[updateIndex]?.body).toEqual({
    metadata: { team: "web", note: "safe", title: "Renamed Session" },
  });
  expect(requests.filter((entry) => entry.method === "GET" && entry.path.endsWith("/events"))).toHaveLength(streamReadsBefore);

  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Session title", { exact: true }).fill("Draft survives 503");
  await controlFixture(request, { sessionUpdateStatus: 503 });
  const postsBefore503 = requests.filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("alert")).toContainText("503");
  await expect(dialog.getByLabel("Session title", { exact: true })).toHaveValue("Draft survives 503");
  await expect(page.locator(".conversation-header h2")).toHaveText("Renamed Session");
  await page.waitForTimeout(350);
  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"))
    .toHaveLength(postsBefore503 + 1);

  await controlFixture(request, { sessionUpdateResponseLoss: 1 });
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("alert")).toContainText("result is unknown");
  await expect(dialog.getByLabel("Session title", { exact: true })).toHaveValue("Draft survives 503");
  await expect(page.locator(".conversation-header h2")).toHaveText("Renamed Session");
  const postsAfterLoss = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await page.waitForTimeout(350);
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  ))).toHaveLength(postsAfterLoss);
});

test("preserves and safely rebases a Session metadata draft after a same-key conflict", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Edit", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Session title", { exact: true }).fill("My preserved draft");
  await setFixtureSessionMetadata(request, "session_snapshot", {
    title: "Concurrent title",
    concurrent: "must survive",
  });

  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("alert")).toContainText("Metadata changed in Agent Core");
  await expect(dialog.getByLabel("Session title", { exact: true })).toHaveValue("My preserved draft");
  await expect(dialog.getByLabel("Additional Session metadata", { exact: true })).toContainText('"concurrent": "must survive"');
  await expect(page.locator(".conversation-header h2")).toHaveText("Concurrent title");
  let writes = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  ));
  expect(writes).toHaveLength(0);

  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("heading", { name: "My preserved draft" })).toBeVisible();
  writes = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  ));
  expect(writes).toHaveLength(1);
  expect(writes[0]?.body).toEqual({
    metadata: { title: "My preserved draft", concurrent: "must survive" },
  });
});

test("rejects wrong-id and deep-malformed Session reads before writes or delete retries", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  await controlFixture(request, { sessionRetrieveVariant: "wrong_id" });
  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toContainText("invalid Session resource");
  await expect(dialog).toContainText("session_snapshot");
  await expect(dialog).not.toContainText("another_session");

  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Session title", { exact: true }).fill("Draft stays local");
  await controlFixture(request, { sessionRetrieveVariant: "deep_malformed" });
  const writesBefore = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog.getByRole("alert")).toContainText("invalid Session retrieval response");
  await expect(dialog.getByLabel("Session title", { exact: true })).toHaveValue("Draft stays local");
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot"
  ))).toHaveLength(writesBefore);

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(dialog).toContainText("Exact Session: session_snapshot");
  await controlFixture(request, {
    sessionDeleteResponseLoss: 2,
    sessionRetrieveVariant: "deep_malformed",
  });
  const deletesBefore = (await fixtureRequests(request)).filter((entry) => entry.method === "DELETE").length;
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog.getByRole("alert")).toContainText("follow-up durable Session refresh also failed");
  await expect(dialog.getByRole("button", { name: "Delete Session" })).toBeDisabled();
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "DELETE"))
    .toHaveLength(deletesBefore + 1);
});

test("requires confirmation and reconciles unknown Session deletes once without retrying the write", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  const manage = page.locator(".conversation-session-action");
  await manage.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(dialog).toContainText("Exact Session: session_snapshot");
  await expect(dialog).toContainText("not a promise of physical history erasure");
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog.getByRole("button", { name: "Edit", exact: true })).toBeFocused();
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "DELETE")).toHaveLength(0);

  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await controlFixture(request, { sessionDeleteStatus: 409 });
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog.getByRole("alert")).toContainText("compatible Core rejected");
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");

  await controlFixture(request, { sessionDeleteStatus: 503 });
  const readsBefore503 = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog.getByRole("alert")).toContainText("503");
  await expect(dialog.getByRole("alert")).toContainText("refresh confirmed that the Session is still present");
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
  let requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE")).toHaveLength(2);
  expect(requests.filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  ))).toHaveLength(readsBefore503 + 1);

  await controlFixture(request, {
    sessionDeleteResponseLoss: 2,
    sessionRetrieveStatus: 503,
  });
  const readsBeforeUnresolved = requests.filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog.getByRole("alert")).toContainText("result is unknown");
  await expect(dialog.getByRole("alert")).toContainText("follow-up durable Session refresh also failed");
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
  await expect(dialog.getByRole("button", { name: "Delete Session" })).toBeDisabled();
  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE")).toHaveLength(3);
  expect(requests.filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  ))).toHaveLength(readsBeforeUnresolved + 1);
  await page.waitForTimeout(350);
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "DELETE")).toHaveLength(3);

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeDisabled();
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await manage.click();
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await controlFixture(request, { sessionDeleteResponseLoss: 1 });
  const readsBeforeAppliedLoss = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  )).length;
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".session-row").filter({ hasText: "Lifecycle Agent" })).toHaveCount(0);
  requests = await fixtureRequests(request);
  expect(requests.filter((entry) => entry.method === "DELETE")).toHaveLength(4);
  expect(requests.filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot"
  ))).toHaveLength(readsBeforeAppliedLoss + 1);
  await page.waitForTimeout(350);
  expect((await fixtureRequests(request)).filter((entry) => entry.method === "DELETE")).toHaveLength(4);
});

test("keeps a stale Session row and surfaces each explicit repeated 404 deletion", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  const manage = page.locator(".conversation-session-action");
  await manage.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await removeFixtureSession(request, "session_snapshot");
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();

  for (const expectedDeletes of [1, 2]) {
    await dialog.getByRole("button", { name: "Delete Session" }).click();
    await expect(dialog.getByRole("alert")).toContainText("not found in Agent Core");
    await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
    expect((await fixtureRequests(request)).filter((entry) => entry.method === "DELETE")).toHaveLength(expectedDeletes);
  }
});

test("deletes an inactive Session without disturbing the active composer or live event stream", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await page.getByRole("button", { name: "Agents" }).click();
  await startSessionWithSecondAgent(page);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  await expect(connectedLiveEvents(page)).toBeVisible();
  const composer = page.getByLabel("Message the Agent");
  await composer.fill("active draft must survive");
  const before = await fixtureState(request);
  const activeId = before.sessions.find((session) => session.id !== "session_snapshot")?.id;
  expect(activeId).toBeTruthy();
  const activeStreamReads = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${activeId}/events`
  )).length;

  const inactiveRow = page.locator(".session-row").filter({ hasText: "Lifecycle Agent" });
  await inactiveRow.locator(".session-row-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await controlFixture(request, { sessionDeleteDelayMs: 1_500 });
  const deleteButton = dialog.locator(".modal-footer .button.danger");
  const deleteClick = deleteButton.click();
  await expect(deleteButton).toBeDisabled();
  await expect(deleteButton).toHaveText("Deleting…");
  await expect(inactiveRow).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeVisible();
  await deleteClick;
  await expect(dialog).toHaveCount(0);
  await expect(inactiveRow).toHaveCount(0);
  await expect(composer).toHaveValue("active draft must survive");
  await expect(connectedLiveEvents(page)).toBeVisible();
  await expect(page.locator(".conversation-session-action")).toBeFocused();

  const after = await fixtureState(request);
  expect(after.aborts.streams).toBe(before.aborts.streams);
  expect(after.openStreams).toContain(activeId);
  expect((await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === `/v1/agents/sessions/${activeId}/events`
  ))).toHaveLength(activeStreamReads);
});

test("continues a Session with a new Turn after its latest attempt fails", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  await emitSessionFixture(request, "failed");
  await expect(page.getByText("Latest attempt failed", { exact: true })).toBeVisible();
  await expect(page.locator(".session-runtime-error")).toContainText(
    "You can send another message to start a new Turn in this Session.",
  );

  const composer = page.getByLabel("Message the Agent");
  await expect(composer).toBeEnabled();
  await composer.fill("Continue after the failed Turn");
  const send = page.getByRole("button", { name: "Send message" });
  await expect(send).toBeEnabled();
  await send.click();

  await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot/events"
  )).length).toBe(1);
  const submission = (await fixtureRequests(request)).find((entry) => (
    entry.method === "POST" && entry.path === "/v1/agents/sessions/session_snapshot/events"
  ));
  expect(submission?.body).toEqual({
    events: [{
      type: "agent.session.input.message",
      input: [{
        role: "user",
        content: [{ type: "input_text", text: "Continue after the failed Turn" }],
      }],
    }],
  });
});

for (const pendingRead of [
  {
    label: "Session",
    path: "/v1/agents/sessions/session_snapshot",
    control: { sessionRetrieveDelayMs: 5_000 },
  },
  {
    label: "Item",
    path: "/v1/agents/sessions/session_snapshot/items",
    control: { itemsRetrieveDelayMs: 5_000 },
  },
  {
    label: "Turn",
    path: "/v1/agents/sessions/session_snapshot/turns",
    control: { turnsRetrieveDelayMs: 5_000 },
  },
] as const) {
  test(`aborts the selected Session's pending ${pendingRead.label} read and SSE after confirmed delete`, async ({ page, request }) => {
    const failedReads = new Map<string, string>();
    page.on("requestfailed", (failedRequest) => {
      const path = new URL(failedRequest.url()).pathname;
      if (failedRequest.method() === "GET") {
        failedReads.set(path, failedRequest.failure()?.errorText ?? "unknown failure");
      }
    });
    await resetFixture(request);
    await openSessionsFromHome(page);
    await expect(connectedLiveEvents(page)).toBeVisible();
    await page.getByRole("button", { name: "Agents" }).click();
    await startSessionWithSecondAgent(page);
    await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
    await controlFixture(request, { turnsScenario: 1 });
    await page.locator(".session-row").filter({ hasText: "Lifecycle Agent" }).locator(".session-row-select").click();
    await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
    await expect(connectedLiveEvents(page)).toBeVisible();
    await expect(page.getByText("Completed Turn output remains in the conversation.")).toBeVisible();
    await page.locator(".conversation-session-action").click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
    await dialog.getByRole("button", { name: "Delete", exact: true }).click();
    const before = await fixtureState(request);
    const previousReads = (await fixtureRequests(request)).filter((entry) => (
      entry.method === "GET" && entry.path === pendingRead.path
    )).length;
    await controlFixture(request, pendingRead.control);
    await emitTurnFixture(request, "completed");
    await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
      entry.method === "GET" && entry.path === pendingRead.path
    )).length).toBeGreaterThan(previousReads);

    await controlFixture(request, { sessionDeleteStreamCloseDelayMs: 3_000 });
    await dialog.getByRole("button", { name: "Delete Session" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
    await expect(page.getByText("Completed Turn output remains in the conversation.")).toHaveCount(0);
    await expect(page.locator('[data-turn-id="turn_completed"]')).toHaveCount(0);
    await expect(page.locator(".conversation-session-action")).toBeFocused();
    await expect(connectedLiveEvents(page)).toBeVisible();

    await expect.poll(() => failedReads.get(pendingRead.path)).toContain("ERR_ABORTED");
    await expect.poll(async () => (await fixtureState(request)).aborts.streams).toBeGreaterThan(before.aborts.streams);
    const after = await fixtureState(request);
    expect(after.sessions.some((session) => session.id === "session_snapshot")).toBe(false);
    expect(after.openStreams).not.toContain("session_snapshot");
  });
}

test("aborts a pending manual recovery read after deleting the selected Session", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await controlFixture(request, { sessionRetrieveDelayMs: 5_000 });

  await expectSelectedDeleteAbortsSessionRead(page, request, () => (
    page.getByRole("button", { name: "Recover durable state" }).click()
  ));
});

test("aborts a pending detail retry read after deleting the selected Session", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await controlFixture(request, { sessionRetrieveStatus: 503 });
  await page.getByRole("button", { name: "Recover durable state" }).click();
  const detailError = page.locator(".session-detail-error");
  await expect(detailError).toBeVisible();
  await controlFixture(request, { sessionRetrieveDelayMs: 5_000 });

  await expectSelectedDeleteAbortsSessionRead(page, request, () => (
    detailError.getByRole("button", { name: "Retry" }).click()
  ));
});

test("deletes the selected Session while its SSE is still connecting", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await page.getByRole("button", { name: "Agents" }).click();
  await startSessionWithSecondAgent(page);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  await expect(connectedLiveEvents(page)).toBeVisible();

  await controlFixture(request, { streamOpenDelayMs: 3_000 });
  const streamReadsBefore = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot/events"
  )).length;
  await page.locator(".session-row").filter({ hasText: "Lifecycle Agent" }).locator(".session-row-select").click();
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");
  await expect(page.getByText("Connecting events…", { exact: true })).toBeVisible();
  await expect.poll(async () => (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path === "/v1/agents/sessions/session_snapshot/events"
  )).length).toBeGreaterThan(streamReadsBefore);
  const before = await fixtureState(request);

  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Delete", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await dialog.getByRole("button", { name: "Delete Session" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  await expect.poll(async () => (await fixtureState(request)).aborts.streams).toBeGreaterThan(before.aborts.streams);
  expect((await fixtureState(request)).sessions.some((session) => session.id === "session_snapshot")).toBe(false);
});

test("keeps Session actions accessible and contained at 390 px in dark mode", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await page.getByRole("button", { name: "Dark theme" }).click();
  const manage = page.locator(".conversation-session-action");
  await manage.focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  const metrics = await dialog.evaluate((element) => {
    const box = element.getBoundingClientRect();
    const footer = element.querySelector(".modal-footer")?.getBoundingClientRect();
    return {
      viewportWidth: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      left: box.left,
      right: box.right,
      bottom: box.bottom,
      footerBottom: footer?.bottom ?? Number.POSITIVE_INFINITY,
    };
  });
  expect(metrics.documentWidth).toBeLessThanOrEqual(metrics.viewportWidth);
  expect(metrics.left).toBeGreaterThanOrEqual(0);
  expect(metrics.right).toBeLessThanOrEqual(390);
  expect(metrics.bottom).toBeLessThanOrEqual(844);
  expect(metrics.footerBottom).toBeLessThanOrEqual(metrics.bottom);

  await expect(dialog.getByRole("button", { name: "Edit", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog.getByRole("button", { name: "Edit", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(manage).toBeFocused();
});

test("presents Dashboard page-chain results and System boundaries without extra detail reads", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await expect(page.getByText("Session is ready", { exact: true })).toBeVisible();
  const initialDetailPaths = [
    "/v1/agents/sessions/session_snapshot",
    "/v1/agents/sessions/session_snapshot/items",
    "/v1/agents/sessions/session_snapshot/turns",
  ];
  await expect.poll(async () => {
    const entries = await fixtureRequests(request);
    return initialDetailPaths.every((path) => entries.filter((entry) => (
      entry.method === "GET" && entry.path === path
    )).length >= 2);
  }).toBe(true);
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();

  const dashboard = page.locator(".dashboard-page");
  await expect(dashboard.getByRole("heading", { name: /Dashboard/ })).toBeVisible();
  await expect(dashboard).toContainText("Latest complete paginated reads");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Agents" })).toContainText("3");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Sessions" })).toContainText("1");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "In progress" })).toContainText("0");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Needs attention" })).toContainText("0");
  await expect(dashboard).not.toContainText("Reported aggregate tokens");
  await expect(dashboard).toContainText("No Sessions currently need attention.");
  await expect(dashboard.getByRole("button", { name: /Create agent/ })).toBeVisible();
  await expect(dashboard.getByRole("button", { name: /Start session/ })).toBeVisible();

  const before = await fixtureRequests(request);
  const count = (entries: FixtureRequest[], path: string) => entries.filter((entry) => (
    entry.method === "GET" && entry.path === path
  )).length;
  const detailPaths = [
    "/v1/agents/sessions/session_snapshot",
    "/v1/agents/sessions/session_snapshot/items",
    "/v1/agents/sessions/session_snapshot/turns",
  ];
  const refresh = dashboard.getByRole("button", { name: "Refresh Dashboard snapshot" });
  await refresh.click();
  await expect.poll(async () => {
    const entries = await fixtureRequests(request);
    return [count(entries, "/v1/agents"), count(entries, "/v1/agents/sessions")];
  }).toEqual([count(before, "/v1/agents") + 1, count(before, "/v1/agents/sessions") + 1]);
  const after = await fixtureRequests(request);
  expect(count(after, "/v1/agents")).toBe(count(before, "/v1/agents") + 1);
  expect(count(after, "/v1/agents/sessions")).toBe(count(before, "/v1/agents/sessions") + 1);
  for (const path of detailPaths) expect(count(after, path)).toBe(count(before, path));
  await attachScreenshot(page, testInfo, "desktop-dashboard-loaded-snapshot");

  await page.setViewportSize({ width: 390, height: 844 });
  const dashboardBounds = await dashboard.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      viewportWidth: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
      left: box.left,
      right: box.right,
    };
  });
  expect(dashboardBounds.documentWidth).toBeLessThanOrEqual(dashboardBounds.viewportWidth);
  expect(dashboardBounds.bodyWidth).toBeLessThanOrEqual(dashboardBounds.viewportWidth);
  expect(dashboardBounds.left).toBeGreaterThanOrEqual(0);
  expect(dashboardBounds.right).toBeLessThanOrEqual(390);
  await expect(dashboard.getByRole("button", { name: /Create agent/ })).toBeVisible();
  await expect(dashboard.getByRole("button", { name: /Start session/ })).toBeVisible();
  await attachScreenshot(page, testInfo, "narrow-dashboard-loaded-snapshot");
  await page.setViewportSize({ width: 1280, height: 720 });

  await dashboard.getByRole("button", { name: /Create agent/ }).click();
  await expect(page.locator(".agent-setup-page").getByRole("heading", { name: "New Agent" })).toBeVisible();
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  await dashboard.getByRole("button", { name: /Start session/ }).click();
  const sessionDialog = page.getByRole("dialog", { name: "Create a Session" });
  await expect(sessionDialog).toBeVisible();
  await sessionDialog.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();

  await dashboard.getByRole("table", { name: "Recent Sessions" }).getByRole("button", { name: "Lifecycle Agent" }).click();
  await expect(page.locator(".session-page")).toBeVisible();
  await expect(page.getByText("Lifecycle Agent", { exact: true }).first()).toBeVisible();

  await page.getByRole("button", { name: "System", exact: true }).click();
  const system = page.locator(".system-page");
  await expect(system.getByRole("listitem").filter({ hasText: "Core API" })).toContainText("Available");
  await expect(system.getByRole("listitem").filter({ hasText: "Vaults" })).toContainText("Available");
  await expect(system.getByRole("listitem").filter({ hasText: "Self-hosted" })).toContainText("Enabled");
  await expect(system.getByRole("listitem").filter({ hasText: "Runtime status" })).toContainText("Cannot be pre-checked");
  await expect(system.getByRole("listitem")).toHaveCount(4);
  await expect(system).not.toContainText("Source Files");
  await expect(system).not.toContainText("Public capability surface");

  const beforeSystemRefresh = await fixtureRequests(request);
  const systemRefresh = system.getByRole("button", { name: "Refresh System status" });
  await systemRefresh.click();
  await expect.poll(async () => {
    const entries = await fixtureRequests(request);
    return [count(entries, "/v1/agents"), count(entries, "/v1/agents/sessions")];
  }).toEqual([
    count(beforeSystemRefresh, "/v1/agents") + 1,
    count(beforeSystemRefresh, "/v1/agents/sessions") + 1,
  ]);
  const afterSystemRefresh = await fixtureRequests(request);
  expect(count(afterSystemRefresh, "/v1/agents")).toBe(count(beforeSystemRefresh, "/v1/agents") + 1);
  expect(count(afterSystemRefresh, "/v1/agents/sessions")).toBe(count(beforeSystemRefresh, "/v1/agents/sessions") + 1);
  for (const path of detailPaths) expect(count(afterSystemRefresh, path)).toBe(count(beforeSystemRefresh, path));
  await attachScreenshot(page, testInfo, "desktop-system-contract-boundary");

  await page.setViewportSize({ width: 390, height: 844 });
  const systemBounds = await system.evaluate((element) => {
    const rows = [...element.querySelectorAll(".system-summary-cell")].map((row) => row.getBoundingClientRect());
    return {
      viewportWidth: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      rowBounds: rows.map((row) => ({ top: row.top, bottom: row.bottom, height: row.height })),
    };
  });
  expect(systemBounds.documentWidth).toBeLessThanOrEqual(systemBounds.viewportWidth);
  for (let index = 1; index < systemBounds.rowBounds.length; index += 1) {
    expect(systemBounds.rowBounds[index]!.top).toBeGreaterThanOrEqual(systemBounds.rowBounds[index - 1]!.bottom);
  }
  expect(systemBounds.rowBounds.every((row) => row.height >= 36)).toBe(true);
  await attachScreenshot(page, testInfo, "narrow-system-contract-boundary");
});

test("publishes Dashboard counts only after every top-level Agent and Session page loads", async ({ page, request }) => {
  await resetFixture(request);
  const agentAfters: Array<string | null> = [];
  const sessionAfters: Array<string | null> = [];
  let sessionTemplate: Record<string, unknown> | null = null;

  await page.route("**/v1/agents**", async (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() !== "GET" || !["/v1/agents", "/v1/agents/sessions"].includes(url.pathname)) {
      await route.continue();
      return;
    }

    const upstream = await route.fetch();
    const payload = await upstream.json() as {
      object: string;
      data: Array<Record<string, unknown>>;
    };
    const after = url.searchParams.get("after");
    expect(url.searchParams.get("limit")).toBe("100");
    expect(url.searchParams.get("order")).toBe("desc");

    if (url.pathname === "/v1/agents") {
      agentAfters.push(after);
      const data = after === null ? payload.data.slice(0, 2) : payload.data.slice(2);
      await route.fulfill({
        response: upstream,
        json: {
          object: "list",
          data,
          has_more: after === null,
          first_id: data[0]?.id ?? null,
          last_id: data.at(-1)?.id ?? null,
        },
      });
      return;
    }

    sessionAfters.push(after);
    if (after === null) sessionTemplate = payload.data[0] ?? null;
    if (sessionTemplate === null) throw new Error("The fixture did not provide a Session pagination template.");
    const original = sessionTemplate;
    const second = {
      ...original,
      id: "session_second_page",
      metadata: { fixture: "second-page" },
      created_at: Number(original?.created_at ?? 0) - 1,
      last_active_at: Number(original?.last_active_at ?? 0) - 1,
    };
    const data = after === null ? [original] : [second];
    await route.fulfill({
      response: upstream,
      json: {
        object: "list",
        data,
        has_more: after === null,
        first_id: data[0]?.id ?? null,
        last_id: data.at(-1)?.id ?? null,
      },
    });
  });

  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();
  const dashboard = page.locator(".dashboard-page");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Agents" })).toContainText("3");
  await expect(dashboard.locator(".dashboard-summary > div").filter({ hasText: "Sessions" })).toContainText("2");
  expect(agentAfters).toEqual([null, "agent_b"]);
  expect(sessionAfters).toEqual([null, "session_snapshot"]);
});

test("keeps the previous Dashboard result when pagination exceeds the safety limit", async ({ page, request }) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await page.getByRole("button", { name: "Dashboard", exact: true }).click();

  const dashboard = page.locator(".dashboard-page");
  const loadedAgents = dashboard.locator(".dashboard-summary > div").filter({ hasText: "Agents" });
  await expect(loadedAgents).toContainText("3");

  let template: Record<string, unknown> | null = null;
  let reads = 0;
  await page.route("**/v1/agents**", async (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() !== "GET" || url.pathname !== "/v1/agents") {
      await route.continue();
      return;
    }
    reads += 1;
    if (template === null) {
      const upstream = await route.fetch();
      const payload = await upstream.json() as { data: Array<Record<string, unknown>> };
      template = payload.data[0] ?? null;
    }
    expect(template).not.toBeNull();
    const id = `agent_safety_page_${reads}`;
    await route.fulfill({
      json: {
        object: "list",
        data: [{ ...template, id, name: `Safety page ${reads}` }],
        has_more: true,
        first_id: id,
        last_id: id,
      },
    });
  });

  const refresh = dashboard.getByRole("button", { name: "Refresh Dashboard snapshot" });
  await refresh.click();
  await expect.poll(() => reads).toBe(100);
  await expect(refresh).toBeEnabled();
  await expect(dashboard).toContainText("Using the last successful snapshot");
  await expect(dashboard).toContainText("collection pagination exceeded the Web safety limit");
  await expect(loadedAgents).toContainText("3");
  await dashboard.getByRole("button", { name: "Connection settings" }).click();
  const connectionDialog = page.getByRole("dialog", { name: "Connect an Agent Core" });
  await expect(connectionDialog).toBeVisible();
  await connectionDialog.getByRole("button", { name: "Cancel" }).click();
  expect(reads).toBe(100);
});

test("renders self-hosted Environment and Workspace state safely across reconnect and narrow themes", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await controlFixture(request, {
    environmentScenario: 1,
    environmentEventStatus: 1,
    environmentEventCount: 1,
    streamCloseCount: 1,
    streamCloseDelayMs: 1_000,
  });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  const { dialog, panel, trigger } = await openEnvironmentDialog(page);
  await expect(trigger).toHaveAccessibleName("Environment pending");
  await expect(panel).toContainText("Self-hosted Environment");
  await expect(panel).toContainText("Pending");
  await expect(panel).toContainText("environment_fixture");
  await expect(panel).toContainText("Workspace is this Environment’s execution directory, not a top-level workspaces API");
  await expect(panel).toContainText("https://executor.example.test/connect");
  await expect(panel.locator('a[href="https://executor.example.test/connect"]')).toHaveCount(0);
  await expect(panel.getByRole("link", { name: "Core setup" })).toBeVisible();
  await expect(panel.getByRole("link", { name: "Launcher setup" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Environment connection required" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Function result required" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Cancel active Turn" })).toBeEnabled();
  await expect(page.locator("body")).not.toContainText("launcher:private");
  await expect(page.locator("body")).not.toContainText("executor_token=secret");
  await expect(page.locator('a[href^="file:"]')).toHaveCount(0);

  await expect.poll(async () => (
    await fixtureRequests(request)
  ).filter((entry) => entry.method === "GET" && entry.path.endsWith("/events")).length).toBeGreaterThanOrEqual(2);
  await expect(panel).toContainText("Pending");
  await expect(panel).toContainText("Status comes from the durable Environment resource");
  await panel.evaluate((element) => element.scrollIntoView({ block: "start" }));
  await attachElementScreenshot(panel, testInfo, "desktop-light-environment-panel");
  await attachScreenshot(page, testInfo, "desktop-light-self-hosted-environment");

  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const { panel: narrowPanel } = await openEnvironmentDialog(page);
  const widths = await narrowPanel.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      viewport: innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
      left: box.left,
      right: box.right,
    };
  });
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.left).toBeGreaterThanOrEqual(0);
  expect(widths.right).toBeLessThanOrEqual(390);
  await narrowPanel.getByRole("link", { name: "Launcher setup" }).focus();
  await expect(narrowPanel.getByRole("link", { name: "Launcher setup" })).toBeFocused();
  await narrowPanel.evaluate((element) => element.scrollIntoView({ block: "start" }));
  await attachElementScreenshot(narrowPanel, testInfo, "narrow-dark-environment-panel");
  await attachScreenshot(page, testInfo, "narrow-dark-self-hosted-environment");

  await controlFixture(request, { environmentScenario: 2, environmentEventStatus: 0 });
  await page.reload();
  const { panel: unknown, trigger: unknownTrigger } = await openEnvironmentDialog(page);
  await expect(unknownTrigger).toHaveAccessibleName("Environment unavailable");
  await expect(unknown).toContainText("Environment unavailable");
  await expect(unknown).toContainText("Unknown type");
  await expect(unknown).not.toContainText("/must-not-render");
  await expect(unknown.locator("a")).toHaveCount(0);

  await controlFixture(request, { environmentScenario: 3, environmentEventStatus: 0 });
  await page.reload();
  await expect(page.locator(".workspace-error")).toContainText("Couldn’t load Sessions");
  await expect(page.locator(".workspace-error")).toContainText("invalid Session resource");
  await expect(environmentTrigger(page)).toHaveCount(0);
});

test("lists Workspace file metadata explicitly, paginates, fails closed, and fences Environment changes", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await controlFixture(request, { environmentScenario: 7 });
  await openSessionsFromHome(page);

  const { panel } = await openEnvironmentDialog(page);
  const files = panel.getByRole("region", { name: "Workspace files" });
  await expect(files).toBeVisible();
  await expect(panel).toContainText("/executor/workspace");
  await expect(files.getByLabel("Directory")).toHaveValue("/executor/workspace");
  await expect(files).toContainText("Files are loaded only when requested");
  expect((await fixtureRequests(request)).filter((entry) => entry.path.endsWith("/files"))).toHaveLength(0);

  await files.getByRole("button", { name: "List files" }).click();
  const table = files.getByRole("table", { name: "Workspace file metadata" });
  await expect(table).toContainText("/executor/workspace/file-01.txt");
  await expect(table).toContainText("/executor/workspace/file-20.txt");
  await expect(table).not.toContainText("/executor/workspace/file-21.txt");
  await expect(files.getByRole("button", { name: "Load more" })).toBeVisible();

  let reads = (await fixtureRequests(request)).filter((entry) => entry.path.endsWith("/files"));
  expect(reads.at(-1)?.query).toBe("?path=%2Fexecutor%2Fworkspace&limit=20&order=asc");
  expect(reads.at(-1)?.beta).toBe("agents=v1");
  await files.getByRole("button", { name: "Load more" }).click();
  await expect(table).toContainText("/executor/workspace/file-21.txt");
  await expect(files.getByRole("button", { name: "Load more" })).toHaveCount(0);
  reads = (await fixtureRequests(request)).filter((entry) => entry.path.endsWith("/files"));
  expect(reads.at(-1)?.query).toBe("?path=%2Fexecutor%2Fworkspace&limit=20&order=asc&page=fixture-page-2");
  await attachElementScreenshot(files, testInfo, "workspace-file-metadata-list");

  await controlFixture(request, { environmentFilesStatus: 503 });
  await files.getByRole("button", { name: "Refresh" }).click();
  await expect(files.getByRole("alert")).toContainText("Workspace files are temporarily unavailable");
  await expect(files.getByRole("table", { name: "Workspace file metadata" })).toHaveCount(0);
  await expect(files).not.toContainText("No direct regular files were returned");

  await controlFixture(request, { environmentFilesStatus: 404 });
  await files.getByRole("button", { name: "Refresh" }).click();
  await expect(files.getByRole("alert")).toContainText("not supported by the connected Core");
  await expect(files.getByRole("table", { name: "Workspace file metadata" })).toHaveCount(0);

  const beforeAbort = (await fixtureState(request)).aborts.environmentFileReads;
  await controlFixture(request, { environmentFilesStatus: 200, environmentFilesDelayMs: 2_000 });
  await files.getByRole("button", { name: "Refresh" }).click();
  await expect.poll(async () => (
    await fixtureRequests(request)
  ).filter((entry) => entry.path.endsWith("/files")).length).toBeGreaterThan(reads.length + 1);
  await controlFixture(request, { environmentScenario: 2 });
  await page.getByRole("button", { name: "Recover durable state" }).evaluate((button) => (
    button as HTMLButtonElement
  ).click());
  await expect(page.getByRole("region", { name: "Workspace files" })).toHaveCount(0);
  await expect.poll(async () => (await fixtureState(request)).aborts.environmentFileReads).toBeGreaterThan(beforeAbort);
});

test("hydrates durable expired and unavailable Environment states without a write or paid Turn", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, {
    environmentScenario: 4,
    environmentResourceStatus: "expired",
    environmentEventStatus: 0,
  });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  let { panel, trigger } = await openEnvironmentDialog(page);
  await expect(trigger).toHaveAccessibleName("Environment expired");
  await expect(panel).toContainText("Expired");
  await expect(panel).toContainText("Environment expired");
  await expect(panel).toContainText("no API-managed files, plugins, or skills");
  await expect(page.getByLabel("Message the Agent")).toBeVisible();

  const initialRequests = await fixtureRequests(request);
  const sessionReadIndex = initialRequests.findIndex(
    (entry) => entry.method === "GET" && /^\/v1\/agents\/sessions\/[^/]+$/.test(entry.path),
  );
  const environmentReadIndex = initialRequests.findIndex(
    (entry) => entry.method === "GET" && isEnvironmentResourcePath(entry.path),
  );
  expect(sessionReadIndex).toBeGreaterThanOrEqual(0);
  expect(environmentReadIndex).toBeGreaterThan(sessionReadIndex);

  let environmentRequests = initialRequests.filter(
    (entry) => isEnvironmentResourcePath(entry.path),
  );
  expect(environmentRequests.length).toBeGreaterThanOrEqual(1);
  expect(environmentRequests.every((entry) => entry.method === "GET" && entry.body === undefined)).toBe(true);

  await controlFixture(request, { environmentRetrieveStatus: 503 });
  await page.reload();
  ({ panel, trigger } = await openEnvironmentDialog(page));
  await expect(trigger).toHaveAccessibleName("Environment unavailable");
  await expect(panel).toContainText("Unavailable");
  await expect(panel).toContainText("conversation remains usable");
  await expect(panel).not.toContainText("Expired");
  await expect(panel).not.toContainText("Connected");
  await expect(page.getByLabel("Message the Agent")).toBeVisible();

  await controlFixture(request, {
    environmentRetrieveStatus: 200,
    environmentResourceVariant: "missing_skills",
  });
  await page.reload();
  ({ panel, trigger } = await openEnvironmentDialog(page));
  await expect(trigger).toHaveAccessibleName("Environment unavailable");
  await expect(panel).toContainText("Unavailable");
  await expect(page.getByLabel("Message the Agent")).toBeVisible();

  environmentRequests = (await fixtureRequests(request)).filter(
    (entry) => isEnvironmentResourcePath(entry.path),
  );
  expect(environmentRequests.every((entry) => entry.method === "GET")).toBe(true);
});

test("hydrates durable Environment state even when the live stream is rejected", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, {
    environmentScenario: 4,
    environmentResourceStatus: "expired",
    streamStatus: 401,
  });
  await openSessionsFromHome(page);

  const { panel, trigger } = await openEnvironmentDialog(page);
  await expect(trigger).toHaveAccessibleName("Environment expired");
  await expect(panel).toContainText("Expired");
  await expect(panel).toContainText("Status comes from the durable Environment resource");
  await expect(page.getByText("Events unavailable", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Message the Agent")).toBeVisible();

  const requests = await fixtureRequests(request);
  expect(requests.some((entry) => (
    entry.method === "GET" && isEnvironmentResourcePath(entry.path)
  ))).toBe(true);
});

test("keeps canonical Environment UUID identity across Session and resource projections", async ({ page, request }) => {
  await resetFixture(request);
  const canonicalEnvironmentUuid = "0f745b0d-b545-49cd-8d7e-4c31c80dc564";
  await controlFixture(request, {
    environmentScenario: 5,
    environmentResourceStatus: "connected",
    environmentEventStatus: 0,
  });
  await openSessionsFromHome(page);

  const { panel, trigger } = await openEnvironmentDialog(page);
  await expect(trigger).toHaveAccessibleName("Environment connected");
  await expect(panel).toContainText("Connected");
  await expect(panel).toContainText("Status comes from the durable Environment resource");
  await expect(panel).not.toContainText("Durable Environment status is unavailable");
  await expect(panel).toContainText(canonicalEnvironmentUuid.toUpperCase());

  const environmentRequests = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "GET" && isEnvironmentResourcePath(entry.path),
  );
  expect(environmentRequests.length).toBeGreaterThanOrEqual(1);
  expect(environmentRequests.every(
    (entry) => entry.path === `/v1/agents/environments/${canonicalEnvironmentUuid.toUpperCase()}` &&
      entry.body === undefined,
  )).toBe(true);
});

test("applies a buffered live Environment event after an earlier durable snapshot", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, {
    environmentScenario: 4,
    environmentResourceStatus: "pending",
    streamStatus: 401,
  });
  await openSessionsFromHome(page);

  await expect(environmentTrigger(page)).toHaveAccessibleName("Environment pending");
  const streamError = page.locator(".session-stream-error");
  await expect(streamError.getByText("Couldn’t open live events")).toBeVisible();

  await controlFixture(request, {
    environmentRetrieveDelayMs: 500,
    environmentEventStatus: 3,
    environmentEventCount: 1,
    streamStatus: 200,
  });
  await streamError.getByRole("button", { name: "Retry" }).click();

  const { panel, trigger } = await openEnvironmentDialog(page);
  await expect(trigger).toHaveAccessibleName("Environment connected");
  await expect(panel).toContainText("Connected");
  await expect(panel).toContainText("last supported live event observed after the durable Environment snapshot");
  await expect(panel).not.toContainText("Pending");
});

test("loads every Turn page, reconciles terminal events, and keeps diagnostics out of Conversation", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await controlFixture(request, {
    turnsScenario: 1,
    turnsPageSize: 2,
  });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  const conversationTab = page.getByRole("tab", { name: "Conversation" });
  const conversation = page.getByRole("tabpanel", { name: "Conversation" });
  await expect(conversation.getByRole("region", { name: "Turn timeline" })).toHaveCount(0);

  await page.getByRole("tab", { name: "Trace" }).click();
  const trace = page.getByRole("tabpanel", { name: "Trace" });
  const diagnostics = trace.locator("details.trace-turn-diagnostics");
  await expect(diagnostics).not.toHaveAttribute("open", "");
  await diagnostics.locator("summary").click();
  await expect(diagnostics).toHaveAttribute("open", "");

  const timeline = diagnostics.getByRole("region", { name: "Turn timeline" });
  await expect(timeline).toContainText("7 observed Turns");
  for (const status of ["Queued", "In progress", "Waiting", "Completed", "Failed", "Cancelled"]) {
    await expect(timeline.getByRole("img", { name: `Turn status: ${status}` }).first()).toBeVisible();
  }
  await expect(timeline).toContainText("Running ·");
  await expect(timeline.locator('[data-turn-id="turn_completed"]')).toContainText("7s");
  await expect(timeline.getByRole("region", { name: "Session aggregate usage" })).toContainText("26");
  await expect(timeline.locator('[data-turn-id="turn_completed"]').getByRole("group", { name: "Usage for Turn turn_completed" })).toContainText("13");
  const failed = timeline.locator('[data-turn-id="turn_failed"]');
  await expect(failed).toContainText("The execution could not complete.");
  await expect(failed).toContainText("1 linked Item");
  await expect(trace.getByText("Persisted input before the Turn failed.")).toBeVisible();
  await expect(timeline).toContainText("1 Item is not associated with an observed Turn yet.");

  const readsBeforeTerminal = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path.endsWith("/turns")
  ));
  expect(readsBeforeTerminal.length).toBeGreaterThanOrEqual(4);
  expect(readsBeforeTerminal.some((entry) => entry.query === "?limit=100&order=asc")).toBe(true);
  expect(readsBeforeTerminal.some((entry) => entry.query?.includes("after=turn_in_progress"))).toBe(true);
  expect(readsBeforeTerminal.every((entry) => entry.body === undefined)).toBe(true);

  const terminal = timeline.locator('[data-turn-id="turn_terminal_refresh"]');
  await expect(terminal).toHaveAttribute("data-turn-status", "in_progress");
  await emitTurnFixture(request, "completed");
  await expect(terminal).toHaveAttribute("data-turn-status", "completed");
  await expect(terminal).toContainText("Turn usage");
  await expect.poll(async () => (
    await fixtureRequests(request)
  ).filter((entry) => entry.method === "GET" && entry.path.endsWith("/turns")).length).toBeGreaterThan(readsBeforeTerminal.length);

  await controlFixture(request, { turnsRetrieveStatus: 503 });
  await page.getByRole("button", { name: "Recover durable state" }).click();
  await expect(trace.locator(".trace-load-error").filter({ hasText: "Turn history is incomplete" })).toBeVisible();
  await expect(timeline.locator(".turn-timeline-failure")).toContainText("Couldn’t load Turn history");
  await expect(timeline).toContainText("last observed Turn timeline remains visible");
  await expect(trace.getByText("Completed Turn output remains in the conversation.")).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await diagnostics.evaluate((element) => element.scrollIntoView({ block: "start" }));
  const diagnosticsLayout = await diagnostics.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      viewport: innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
      left: box.left,
      right: box.right,
      overflowY: getComputedStyle(element).overflowY,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
    };
  });
  expect(diagnosticsLayout.document).toBeLessThanOrEqual(diagnosticsLayout.viewport);
  expect(diagnosticsLayout.body).toBeLessThanOrEqual(diagnosticsLayout.viewport);
  expect(diagnosticsLayout.left).toBeGreaterThanOrEqual(0);
  expect(diagnosticsLayout.right).toBeLessThanOrEqual(diagnosticsLayout.viewport);
  expect(diagnosticsLayout.overflowY).toBe("auto");
  expect(diagnosticsLayout.scrollHeight).toBeGreaterThan(diagnosticsLayout.clientHeight);
  await diagnostics.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect(timeline.locator(".turn-card").last()).toBeVisible();
  await attachElementScreenshot(diagnostics, testInfo, "narrow-turn-diagnostics");

  await conversationTab.click();
  await expect(conversation.getByRole("region", { name: "Turn timeline" })).toHaveCount(0);
  await expect(conversation.getByText("Completed Turn output remains in the conversation.")).toBeVisible();
  await expect(page.getByLabel("Message the Agent")).toBeVisible();
});

test("presents an honest searchable Trace workbench without changing the conversation draft", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await controlFixture(request, {
    turnsScenario: 1,
    turnsPageSize: 2,
    itemsScenario: 2,
  });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  const viewTabs = page.getByRole("tablist", { name: "Session view" });
  const conversationTab = viewTabs.getByRole("tab", { name: "Conversation" });
  const traceTab = viewTabs.getByRole("tab", { name: "Trace" });
  const composer = page.getByLabel("Message the Agent");
  await expect(page.locator("#session-trace-panel")).toBeHidden();
  await composer.fill("Draft survives Trace inspection\nwith a second line");
  const sendsBefore = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length;

  await conversationTab.focus();
  await page.keyboard.press("ArrowRight");
  await expect(traceTab).toHaveAttribute("aria-selected", "true");
  const trace = page.getByRole("tabpanel", { name: "Trace" });
  await expect(trace).toBeVisible();
  await expect(trace).toContainText("Known Turn time");
  const diagnostics = trace.locator("details.trace-turn-diagnostics");
  await expect(diagnostics).not.toHaveAttribute("open", "");
  await expect(diagnostics.locator("summary")).toContainText("Turn diagnostics");
  await expect(trace).toContainText("Turns");
  await expect(trace).toContainText("Tool calls");
  await expect(trace).toContainText("Equal-width sequence · not time-scaled");
  await expect(trace.locator(".trace-order-scroll")).toHaveCount(1);
  await expect(trace).toContainText("Core reports Turn wall-clock time, but not per-item timing");
  await expect(trace).toContainText("Configured instructions");
  await expect(trace).toContainText("Completed Turn output remains in the conversation.");
  await expect(trace).not.toContainText("TTFT");
  await expect(trace).not.toContainText("Throughput");

  const search = trace.getByRole("searchbox", { name: "Search trace" });
  await search.fill("Persisted input failed");
  await expect(trace.locator(".trace-ledger-row")).toHaveCount(1);
  await expect(trace).toContainText("Persisted input before the Turn failed.");
  const messageDuration = trace.locator(".trace-ledger-row").filter({ hasText: "Persisted input before the Turn failed." }).locator(".trace-row-duration");
  await expect(messageDuration.locator('[aria-hidden="true"]')).toHaveText("—");
  await expect(messageDuration).toHaveAttribute("title", "Core does not provide per-item timing.");
  await expect(messageDuration.locator(".trace-visually-hidden")).toHaveText("Per-item timing not provided by Core.");
  await search.fill("");

  const patchRow = trace.locator(".trace-ledger-row-tools").filter({ hasText: "apply_patch" }).first();
  await expect(patchRow.locator('.trace-row-duration [aria-hidden="true"]')).toHaveText("—");
  await expect(patchRow.locator(".trace-row-duration")).toHaveAttribute("title", "Core does not provide per-item timing.");
  await patchRow.click();
  const detail = page.getByRole("complementary", { name: "Trace item details" });
  await expect(detail).toBeVisible();
  await expect(detail.getByRole("button", { name: "Close trace details" })).toBeFocused();
  const previewTab = detail.getByRole("tab", { name: "Preview" });
  await expect(previewTab).toHaveAttribute("aria-controls", "session-trace-panel-detail-content");
  await previewTab.click();
  await expect(detail.getByRole("tabpanel", { name: "Preview" })).toHaveAttribute("tabindex", "0");
  const viewer = detail.getByRole("region", { name: "Parsar apply patch diff" });
  await expect(viewer).toContainText("3 files");
  await expect(viewer).toContainText("Completed");
  const desktopSplit = await Promise.all([
    trace.locator(".trace-ledger").boundingBox(),
    detail.boundingBox(),
  ]);
  expect(desktopSplit[0]).not.toBeNull();
  expect(desktopSplit[1]).not.toBeNull();
  expect(desktopSplit[1]!.x).toBeGreaterThanOrEqual(desktopSplit[0]!.x + desktopSplit[0]!.width - 1);
  await attachScreenshot(page, testInfo, "desktop-trace-detail");
  await page.keyboard.press("Escape");
  await expect(detail).toHaveCount(0);
  await expect(patchRow).toBeFocused();

  await patchRow.click();
  await search.fill("filter selected row out");
  await expect(patchRow).toBeHidden();
  await page.getByRole("button", { name: "Close trace details" }).click();
  await expect(search).toBeFocused();
  await search.fill("");

  await conversationTab.click();
  await expect(composer).toBeVisible();
  await expect(composer).toHaveValue("Draft survives Trace inspection\nwith a second line");
  const sendsAfter = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length;
  expect(sendsAfter).toBe(sendsBefore);

  await page.setViewportSize({ width: 390, height: 844 });
  await traceTab.click();
  const narrowTrace = page.getByRole("tabpanel", { name: "Trace" });
  const narrowPatchRow = narrowTrace.locator(".trace-ledger-row-tools").filter({ hasText: "apply_patch" }).first();
  await narrowPatchRow.click();
  const narrowDetail = page.getByRole("complementary", { name: "Trace item details" });
  await expect(narrowDetail).toBeVisible();
  await expect(narrowTrace.locator(".trace-ledger")).toBeHidden();
  const widths = await narrowDetail.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return {
      viewport: innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
      left: box.left,
      right: box.right,
    };
  });
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.left).toBeGreaterThanOrEqual(0);
  expect(widths.right).toBeLessThanOrEqual(widths.viewport);
  await attachScreenshot(page, testInfo, "narrow-trace-detail");
});

test("drops a delayed Turn page after switching Sessions", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, {
    turnsScenario: 1,
    turnsRetrieveDelayMs: 700,
    turnsPageSize: 2,
  });
  await openSessionsFromHome(page);
  await expect(page.getByText("Completed Turn output remains in the conversation.")).toBeVisible({ timeout: 1_500 });
  await page.getByRole("tab", { name: "Trace" }).click();
  const diagnostics = page.locator("details.trace-turn-diagnostics");
  await diagnostics.locator("summary").click();
  const timeline = diagnostics.getByRole("region", { name: "Turn timeline" });
  await expect(timeline).toContainText("Loading every Turn page");
  await page.getByRole("button", { name: "Agents" }).click();
  await expect(page.getByRole("list", { name: "Agents", exact: true })).toBeVisible();
  await startSessionWithSecondAgent(page);

  await page.getByRole("tab", { name: "Trace" }).click();
  const nextDiagnostics = page.locator("details.trace-turn-diagnostics");
  await nextDiagnostics.locator("summary").click();
  const nextTimeline = nextDiagnostics.getByRole("region", { name: "Turn timeline" });
  await expect(nextTimeline).toContainText("Queued");
  await page.waitForTimeout(3_000);
  await expect(nextTimeline).not.toContainText("turn_queued");
  await expect(page.getByText("Completed Turn output remains in the conversation.")).toHaveCount(0);

  const turnReads = (await fixtureRequests(request)).filter((entry) => (
    entry.method === "GET" && entry.path.endsWith("/turns")
  ));
  expect(turnReads.some((entry) => entry.path.includes("session_snapshot"))).toBe(true);
  expect(turnReads.some((entry) => entry.path.includes("session_created_"))).toBe(true);
});

test("renders Parsar patches as accessible read-only diffs in desktop and narrow themes", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await controlFixture(request, { itemsScenario: 1 });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();

  const completedTrace = page.locator('[data-work-trace="completed"]');
  await completedTrace.getByRole("button", { name: /Completed/ }).click();
  const completedStep = completedTrace.locator('[data-trace-step="patch_completed"]');
  await completedStep.getByRole("button", { name: /apply_patch/ }).click();
  const viewer = completedStep.getByRole("region", { name: "Parsar apply patch diff" });
  await expect(viewer).toContainText("3 files");
  await expect(viewer).toContainText("Completed");
  await expect(viewer.getByRole("button", { name: /modify src\/modify.ts/ })).toHaveAttribute("aria-expanded", "true");
  await viewer.getByRole("button", { name: /modify src\/modify.ts/ }).focus();
  await page.keyboard.press("Enter");
  await expect(viewer.getByRole("button", { name: /modify src\/modify.ts/ })).toHaveAttribute("aria-expanded", "false");
  await viewer.getByText("Raw arguments").click();
  await viewer.getByText("Raw result").click();
  await expect(viewer).toContainText('"applied": true');
  await expect(viewer.locator("script")).toHaveCount(0);
  const runningStep = page.locator('[data-trace-step="patch_running"]');
  await runningStep.getByRole("button", { name: /apply_patch/ }).click();
  await expect(runningStep.locator('[data-patch-status="in_progress"]')).toContainText("In progress");
  const failedTrace = page.locator('[data-work-trace="failed"]');
  await failedTrace.getByRole("button", { name: /Failed/ }).click();
  const failedStep = failedTrace.locator('[data-trace-step="patch_failed"]');
  await failedStep.getByRole("button", { name: /apply_patch/ }).click();
  await expect(failedStep.locator('[data-patch-status="failed"]')).toContainText("Failed");
  await failedStep.getByText("Raw result").click();
  await expect(failedStep).toContainText("fixture failure");
  const fallback = page.locator('[data-trace-step="patch_alternate"]');
  await fallback.getByRole("button", { name: /apply_patch/ }).click();
  await expect(fallback).toContainText("malformed alternate shape");
  await expect(fallback.getByRole("region", { name: "Parsar apply patch diff" })).toHaveCount(0);
  await attachScreenshot(page, testInfo, "desktop-light-parsar-diff");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Dark theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const widths = await viewer.evaluate((element) => {
    const box = element.getBoundingClientRect();
    return { viewport: innerWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth, viewerLeft: box.left, viewerRight: box.right };
  });
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.viewerLeft).toBeGreaterThanOrEqual(0);
  expect(widths.viewerRight).toBeLessThanOrEqual(widths.viewport);
  await expect(viewer).toBeVisible();
  await attachScreenshot(page, testInfo, "narrow-dark-parsar-diff");
});

test("manually retries uncertain sends with the original key only while the payload is unchanged", async ({ page, request }, testInfo) => {
  await resetFixture(request);
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  const composer = page.getByLabel("Message the Agent");

  await controlFixture(request, { sendResponseLoss: 1 });
  await composer.fill("uncertain payload");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(page.locator(".session-send-error")).toContainText("may have accepted this message");
  await expect(composer).toHaveValue("uncertain payload");
  await attachScreenshot(page, testInfo, "desktop-uncertain-send");
  await page.getByRole("button", { name: "Send message" }).click();

  let sends = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  );
  expect(sends).toHaveLength(2);
  expect(sends[0]?.idempotencyKey).toBeTruthy();
  expect(sends[1]?.idempotencyKey).toBe(sends[0]?.idempotencyKey);

  await controlFixture(request, { sendResponseLoss: 1 });
  await composer.fill("original before edit");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(composer).toHaveValue("original before edit");
  await composer.fill("edited payload");
  await page.getByRole("button", { name: "Send message" }).click();

  sends = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  );
  expect(sends).toHaveLength(4);
  expect(sends[3]?.idempotencyKey).not.toBe(sends[2]?.idempotencyKey);

  await controlFixture(request, { sendStatus: 422 });
  await composer.fill("permanently rejected");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(page.locator(".session-send-error")).toContainText("Agent Core rejected the message");
  await page.getByRole("button", { name: "Send message" }).click();

  sends = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  );
  expect(sends).toHaveLength(6);
  expect(sends[5]?.idempotencyKey).not.toBe(sends[4]?.idempotencyKey);
  await attachScreenshot(page, testInfo, "desktop-send-recovery");
});

test("reuses Function result identity only for an unchanged uncertain explicit retry", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, { environmentScenario: 10 });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  const editor = page.getByLabel("Function result or error");
  const submit = page.getByRole("button", { name: "Submit result" });
  const eventWrites = async () => (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  );

  await editor.fill("uncertain result");
  await controlFixture(request, { sendResponseLoss: 1 });
  await submit.click();
  await expect(editor).toBeEnabled();
  await expect(editor).toHaveValue("uncertain result");
  await submit.click();
  await expect.poll(async () => (await eventWrites()).length).toBe(2);
  let writes = await eventWrites();
  expect(writes[0]?.body).toEqual(writes[1]?.body);
  expect(writes[0]?.idempotencyKey).toBeTruthy();
  expect(writes[1]?.idempotencyKey).toBe(writes[0]?.idempotencyKey);

  await editor.fill("before edit");
  await controlFixture(request, { sendResponseLoss: 1 });
  await submit.click();
  await expect(editor).toBeEnabled();
  await editor.fill("after edit");
  await submit.click();
  await expect.poll(async () => (await eventWrites()).length).toBe(4);
  writes = await eventWrites();
  expect(writes[3]?.idempotencyKey).not.toBe(writes[2]?.idempotencyKey);

  await editor.fill("definite rejection");
  await controlFixture(request, { sendStatus: 422 });
  await submit.click();
  await expect(editor).toBeEnabled();
  await submit.click();
  await expect.poll(async () => (await eventWrites()).length).toBe(6);
  writes = await eventWrites();
  expect(writes[5]?.idempotencyKey).not.toBe(writes[4]?.idempotencyKey);
});

test("keeps cancellation available for an Environment-only required action", async ({ page, request }) => {
  await resetFixture(request);
  await controlFixture(request, { environmentScenario: 6 });
  await openSessionsFromHome(page);
  await expect(connectedLiveEvents(page)).toBeVisible();
  await expect(page.getByRole("region", { name: "Environment connection required" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Function result required" })).toHaveCount(0);
  const writesBefore = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length;
  const cancel = page.getByRole("button", { name: "Cancel active Turn" });
  await expect(cancel).toBeEnabled();
  await controlFixture(request, { sendResponseLoss: 1 });
  await cancel.click();
  await expect.poll(async () => (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length).toBe(writesBefore + 1);
  await expect(cancel).toBeEnabled();
  await cancel.click();
  await expect.poll(async () => (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  ).length).toBe(writesBefore + 2);
  const writes = (await fixtureRequests(request)).filter(
    (entry) => entry.method === "POST" && entry.path.endsWith("/events"),
  );
  expect(writes.at(-1)?.body).toEqual({ events: [{ type: "agent.session.input.cancel" }] });
  expect(writes.at(-2)?.idempotencyKey).toBeTruthy();
  expect(writes.at(-1)?.idempotencyKey).toBe(writes.at(-2)?.idempotencyKey);
});
