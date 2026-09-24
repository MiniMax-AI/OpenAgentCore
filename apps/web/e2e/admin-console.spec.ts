import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

// Acceptance coverage for the administrator console information architecture:
// grouped navigation, the operations Overview, metrics, the resource tables and
// the Playground hand-offs, legacy hashes and the Chinese locale.

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
/** Fixture Session, Turn and Agent timestamps are anchored to this instant. */
const fixtureBaseline = 1_789_438_800;

const expectedGroups = [
  { name: "Monitor", views: ["Overview", "Agent metrics", "Sandbox metrics", "Session log"] },
  { name: "Resources", views: ["Agents", "Environment templates", "Vaults"] },
  { name: "Infrastructure", views: ["Nodes"] },
  { name: "Settings", views: ["API keys", "System"] },
  // "Getting started" joins this group only when the console has an account introduction.
  { name: "Playground", views: ["Session console", "Agent builder"] },
] as const;

const expectedChineseGroups = [
  { name: "监控", views: ["总览", "智能体监控", "沙箱监控", "会话日志"] },
  { name: "资源", views: ["智能体", "环境模板", "凭据库"] },
  { name: "基础设施", views: ["节点"] },
  { name: "设置", views: ["API 密钥", "系统配置"] },
  { name: "调试台", views: ["会话调试", "智能体构建器"] },
] as const;

async function resetFixture(request: APIRequestContext) {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
}

function consoleNav(page: Page, name = "Console navigation") {
  return page.getByRole("navigation", { name, exact: true });
}

function navButton(page: Page, name: string) {
  return consoleNav(page).getByRole("button", { name, exact: true });
}

async function openView(page: Page, name: string) {
  await navButton(page, name).click();
  await expect(navButton(page, name)).toHaveAttribute("aria-current", "page");
}

/** Group label paragraphs and button names in DOM order, one entry per sidebar group. */
async function sidebarStructure(page: Page, navigationName = "Console navigation") {
  return consoleNav(page, navigationName).getByRole("group").evaluateAll((groups) => groups.map((group) => ({
    name: group.querySelector(".nav-label")?.textContent?.trim() ?? "",
    views: [...group.querySelectorAll("button")].map((button) => button.getAttribute("aria-label") ?? button.textContent?.trim() ?? ""),
  })));
}

function overviewKpi(page: Page, label: string) {
  return page.getByLabel("Deployment health", { exact: true }).locator(".kpi")
    .filter({ has: page.locator("dt > span", { hasText: new RegExp(`^${label}$`) }) })
    .locator(".kpi-value");
}

function sessionLog(page: Page) {
  return page.getByRole("region", { name: "Session log", exact: true });
}

function statusFilter(page: Page, label: string) {
  return sessionLog(page).getByRole("group", { name: "Status", exact: true })
    .getByRole("button", { name: new RegExp(`^${label}\\s*\\d*$`) });
}

/** Collects uncaught page errors so each test can prove none occurred. */
function collectPageErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  return errors;
}

async function createSecondAgentSession(request: APIRequestContext) {
  const response = await request.post(`${fixtureBaseUrl}/v1/agents/sessions`, {
    headers: { "OpenAI-Beta": "agents=v1", "Idempotency-Key": "admin-console-second-session" },
    data: {
      agent_id: "agent_b",
      environment: { type: "none" },
      metadata: { fixture: "admin-console" },
      input: "Summarize the deployment.",
      stream: false,
      vault_ids: [],
    },
  });
  expect(response.status()).toBe(201);
}

test.beforeEach(async ({ request }) => {
  await resetFixture(request);
});

test("groups the sidebar by task and marks exactly the current page", async ({ page }) => {
  const errors = collectPageErrors(page);
  await page.goto("/");
  await expect(page.locator(".app-sidebar .brand-name")).toHaveText("Parsar Core");
  await expect(page.locator(".app-sidebar .brand-product")).toHaveText("Console");
  await expect(navButton(page, "Overview")).toBeVisible();
  expect(await sidebarStructure(page)).toEqual(expectedGroups);

  const current = consoleNav(page).locator('button[aria-current="page"]');
  await expect(current).toHaveCount(1);
  await expect(navButton(page, "Overview")).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "Overview", exact: true })).toBeVisible();

  const pages: Array<{ view: string; hash: string; heading: string | RegExp }> = [
    { view: "Agent metrics", hash: "#agent-metrics", heading: "Agent metrics" },
    { view: "Sandbox metrics", hash: "#sandbox-metrics", heading: "Sandbox metrics" },
    { view: "Session log", hash: "#sessions", heading: "Session log" },
    { view: "Agents", hash: "#agents", heading: "Agents" },
    { view: "Environment templates", hash: "#templates", heading: "Environment Templates" },
    { view: "Vaults", hash: "#vaults", heading: "Vaults" },
    { view: "Nodes", hash: "#nodes", heading: "Nodes" },
    { view: "API keys", hash: "#api-keys", heading: "API keys" },
    { view: "System", hash: "#system", heading: "System" },
    { view: "Session console", hash: "#playground", heading: /^Session console\b/ },
    { view: "Agent builder", hash: "#builder", heading: "Agent builder" },
  ];
  for (const target of pages) {
    await openView(page, target.view);
    await expect(current).toHaveCount(1);
    await expect(page).toHaveURL(new RegExp(`${target.hash}$`));
    await expect(page.locator("main").getByRole("heading", { level: 1, name: target.heading, exact: typeof target.heading === "string" })).toBeVisible();
  }

  await openView(page, "Overview");
  await expect(page).toHaveURL(/\/$/);
  expect(errors).toEqual([]);
});

test("legacy Dashboard and Sandbox hashes open Overview and Nodes", async ({ page }) => {
  await page.goto("/#dashboard");
  await expect(navButton(page, "Overview")).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "Overview", exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/$/);

  await page.goto("/#sandbox");
  await expect(navButton(page, "Nodes")).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "Nodes", exact: true })).toBeVisible();
  await expect(page).toHaveURL(/#nodes$/);

  await page.goto("/#not-a-console-page");
  await expect(navButton(page, "Overview")).toHaveAttribute("aria-current", "page");
});

test("Overview shows the KPI strip and fleet, and a selected node lists its allocations", async ({ page }) => {
  const errors = collectPageErrors(page);
  await page.route("**/console/config", (route) => route.fulfill({ json: { sandbox_admin: true, node_installer: false } }));
  await page.goto("/");

  const overview = page.locator(".overview-page");
  const kpis = page.getByLabel("Deployment health", { exact: true });
  await expect(kpis.locator(".kpi dt > span:first-child")).toHaveText([
    "Service",
    "Nodes online",
    "Sandbox slots",
    "Running Sessions",
    "Needs attention",
    "Tokens reported",
  ]);
  await expect(overviewKpi(page, "Nodes online")).toHaveText("1 / 2");
  await expect(overviewKpi(page, "Sandbox slots")).toHaveText("1 / 4");
  // One enrolled node is offline, so the deployment cannot report healthy.
  await expect(overviewKpi(page, "Service")).toHaveText("Degraded");
  await expect(overviewKpi(page, "Running Sessions")).toHaveText("0");
  await expect(overviewKpi(page, "Needs attention")).toHaveText("0");

  const fleet = overview.getByRole("list", { name: "Fleet", exact: true });
  const deployment = fleet.getByRole("button", { name: /Core deployment/ });
  const coreServer = fleet.getByRole("button", { name: /Core server/ });
  const offlineHost = fleet.getByRole("button", { name: /Offline host/ });
  await expect(fleet.getByRole("listitem")).toHaveCount(3);
  await expect(deployment).toHaveAttribute("aria-pressed", "true");
  await expect(coreServer).toHaveAttribute("aria-pressed", "false");
  await expect(offlineHost.getByRole("img", { name: "Offline" })).toBeVisible();

  await coreServer.click();
  await expect(coreServer).toHaveAttribute("aria-pressed", "true");
  await expect(deployment).toHaveAttribute("aria-pressed", "false");
  const detail = overview.locator(".fleet-detail");
  await expect(detail.getByRole("heading", { level: 2, name: "Core server", exact: true })).toBeVisible();
  await expect(detail.getByRole("heading", { name: "Allocations (1)", exact: true })).toBeVisible();
  const allocations = detail.getByRole("table");
  await expect(allocations.getByRole("row")).toHaveCount(2);
  await expect(allocations).toContainText("Active");
  await expect(allocations).toContainText("Running");

  await offlineHost.click();
  await expect(detail.getByRole("heading", { level: 2, name: "Offline host", exact: true })).toBeVisible();
  await expect(detail.getByRole("heading", { name: "Allocations (0)", exact: true })).toBeVisible();
  await expect(detail).toContainText("No sandboxes on this node.");
  await expect(detail).toContainText("Host metrics are unavailable while the heartbeat is stale.");

  // An allocation that belongs to a loaded Session opens it in the Session console.
  await coreServer.click();
  await allocations.getByRole("button", { name: "Lifecycle Agent", exact: true }).click();
  await expect(navButton(page, "Session console")).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".session-row.active")).toContainText("Lifecycle Agent");
  expect(errors).toEqual([]);
});

test("Overview lists failed Sessions under Needs attention and opens them", async ({ page, request }) => {
  await createSecondAgentSession(request);
  expect((await request.post(`${fixtureBaseUrl}/__fixture/emit-session`, { data: { status: "failed" } })).ok()).toBe(true);
  await page.goto("/");

  await expect(overviewKpi(page, "Needs attention")).toHaveText("1");
  const attention = page.getByRole("region", { name: "Needs attention", exact: true });
  const rows = attention.getByRole("table").getByRole("row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(1)).toContainText("Lifecycle Agent");
  await expect(rows.nth(1)).toContainText("Failed");
  await expect(rows.nth(1)).toContainText("The execution could not complete.");
  await expect(attention).not.toContainText("Second Agent");

  await attention.getByRole("button", { name: "Session log", exact: true }).click();
  await expect(navButton(page, "Session log")).toHaveAttribute("aria-current", "page");
  await openView(page, "Overview");
  await rows.nth(1).getByRole("button", { name: "Lifecycle Agent", exact: true }).click();
  await expect(navButton(page, "Session console")).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".session-row.active")).toContainText("Lifecycle Agent");
});

test("Agent metrics reads the fixture Session and shows totals or an explicit empty range", async ({ page }) => {
  const errors = collectPageErrors(page);
  // Pin the browser clock beside the fixture's Turns so the shorter ranges include them.
  await page.clock.setFixedTime(new Date((fixtureBaseline + 120) * 1_000));
  await page.goto("/#agent-metrics");

  const metrics = page.locator(".metrics-page");
  await expect(metrics.getByRole("heading", { level: 1, name: "Agent metrics", exact: true })).toBeVisible();
  const ranges = metrics.getByRole("radiogroup", { name: "Time range", exact: true });
  await expect(ranges.getByRole("radio")).toHaveText(["1 hour", "6 hours", "24 hours", "7 days"]);
  await expect(ranges.getByRole("radio", { name: "24 hours" })).toHaveAttribute("aria-checked", "true");

  const totals = metrics.getByLabel("Agent run totals", { exact: true });
  const empty = metrics.locator(".console-empty").filter({ hasText: "No Agent runs in this range" });
  for (const range of ["1 hour", "6 hours", "24 hours", "7 days"]) {
    await ranges.getByRole("radio", { name: range, exact: true }).click();
    await expect(ranges.getByRole("radio", { name: range, exact: true })).toHaveAttribute("aria-checked", "true");
    await expect(metrics.getByRole("status").filter({ hasText: "Reading recent Turns…" })).toHaveCount(0, { timeout: 15_000 });
    await expect(totals.or(empty)).toBeVisible();
    if (await totals.isVisible()) {
      await expect(totals.locator(".kpi dt > span").first()).toHaveText("Requests");
      await expect(totals.locator(".kpi-value").first()).toHaveText(/^\d[\d,]*$/);
    } else {
      await expect(empty).toContainText(`No Turn was created in the last ${range}`);
    }
    await expect(metrics).not.toContainText("could not be loaded");
    await expect(metrics).not.toContainText("could not be read");
  }
  expect(errors).toEqual([]);
});

test("Session log filters by status and opens a row in the Session console", async ({ page, request }) => {
  const errors = collectPageErrors(page);
  await createSecondAgentSession(request);
  expect((await request.post(`${fixtureBaseUrl}/__fixture/emit-session`, { data: { status: "failed" } })).ok()).toBe(true);
  await page.goto("/");
  await openView(page, "Session log");

  const table = sessionLog(page).getByRole("table");
  await expect(statusFilter(page, "All")).toHaveAttribute("aria-pressed", "true");
  await expect(statusFilter(page, "All").locator(".filter-count")).toHaveText("2");
  await expect(statusFilter(page, "Failed").locator(".filter-count")).toHaveText("1");
  await expect(table.getByRole("row")).toHaveCount(3);

  await statusFilter(page, "Failed").click();
  await expect(statusFilter(page, "Failed")).toHaveAttribute("aria-pressed", "true");
  await expect(statusFilter(page, "All")).toHaveAttribute("aria-pressed", "false");
  await expect(table.getByRole("row")).toHaveCount(2);
  const failedRow = table.getByRole("row").filter({ hasText: "Lifecycle Agent" });
  await expect(failedRow).toContainText("Failed");
  await expect(failedRow.getByRole("button", { name: "Error", exact: true })).toBeVisible();
  await expect(failedRow).toContainText("The execution could not complete.");
  await expect(table).not.toContainText("Second Agent");
  await expect(sessionLog(page)).toContainText("Showing 1 of 1");

  await statusFilter(page, "Running").click();
  await expect(table.getByRole("row")).toHaveCount(2);
  await expect(table).toContainText("Second Agent");
  await expect(table).not.toContainText("Lifecycle Agent");

  await statusFilter(page, "Idle").click();
  await expect(table).toHaveCount(0);
  await expect(sessionLog(page)).toContainText("No matching Sessions");

  await statusFilter(page, "All").click();
  await sessionLog(page).getByRole("searchbox", { name: "Search by ID, Agent, model or error" }).fill("could not complete");
  await expect(table.getByRole("row")).toHaveCount(2);
  await expect(table).toContainText("Lifecycle Agent");

  // The newest Session is selected by default, so opening the failed one proves the hand-off.
  // Click a plain cell: the row itself is the target, not only its name button.
  await failedRow.getByRole("cell", { name: "No environment", exact: true }).click();
  await expect(navButton(page, "Session console")).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "Session console" })).toBeVisible();
  await expect(page.locator(".session-row.active")).toHaveCount(1);
  await expect(page.locator(".session-row.active")).toContainText("Lifecycle Agent");
  await expect(page.locator(".conversation-header h2")).toHaveText("Lifecycle Agent");

  await openView(page, "Session log");
  await sessionLog(page).getByRole("button", { name: "Second Agent", exact: true }).click();
  await expect(navButton(page, "Session console")).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".session-row.active")).toContainText("Second Agent");
  await expect(page.locator(".conversation-header h2")).toHaveText("Second Agent");
  expect(errors).toEqual([]);
});

test("Agents table is read-only and Edit opens that Agent in the Agent builder", async ({ page, request }) => {
  await page.goto("/");
  await openView(page, "Agents");
  const agents = page.getByRole("region", { name: "Agents", exact: true });
  const table = agents.getByRole("table");
  await expect(table.getByRole("row")).toHaveCount(4);
  await expect(agents).toContainText("3 of 3 Agents");
  await expect(agents.getByRole("textbox")).toHaveCount(0);
  await expect(agents.getByRole("button", { name: /^(Delete|Save)/ })).toHaveCount(0);

  const lifecycleRow = table.getByRole("row").filter({ has: page.getByRole("rowheader", { name: /Lifecycle Agent/ }) });
  await expect(lifecycleRow.getByRole("rowheader")).toHaveAttribute("title", "agent_a");
  await expect(lifecycleRow.getByRole("button", { name: "Sessions", exact: true })).toBeEnabled();

  const secondRow = table.getByRole("row").filter({ has: page.getByRole("rowheader", { name: "Second Agent", exact: true }) });
  await expect(secondRow).toContainText("fixture/model-b");
  await expect(secondRow.getByRole("button", { name: "Sessions", exact: true })).toBeDisabled();
  const postsBefore = (await (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json() as Array<{ method: string }>)
    .filter((entry) => entry.method !== "GET").length;
  await secondRow.getByRole("button", { name: "Edit", exact: true }).click();

  await expect(navButton(page, "Agent builder")).toHaveAttribute("aria-current", "page");
  const setup = page.locator(".agent-setup-page");
  await expect(setup.getByRole("heading", { name: "Second Agent", exact: true })).toBeVisible();
  await expect(setup.getByRole("region", { name: "Saved definition", exact: true })).toContainText("agent_b");
  await expect(page.getByLabel("Name")).toHaveValue("Second Agent");
  // Opening the editor reads the latest definition but never writes.
  const writes = (await (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json() as Array<{ method: string }>)
    .filter((entry) => entry.method !== "GET").length;
  expect(writes).toBe(postsBefore);

  await page.getByRole("button", { name: "Back to Agents" }).click();
  await expect(page.getByRole("table", { name: "Agents", exact: true })).toBeVisible();

  // "New in builder" hands one create request to the Agent builder; it is consumed once.
  await openView(page, "Agents");
  await agents.getByRole("button", { name: "New in builder", exact: true }).click();
  await expect(navButton(page, "Agent builder")).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".agent-setup-page").getByRole("heading", { name: "New Agent" })).toBeVisible();
  await expect(page.getByLabel("Name")).toHaveValue("");
  await openView(page, "Agents");
  await openView(page, "Agent builder");
  await expect(page.getByRole("table", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "New Agent" })).toHaveCount(0);
  const writesAfterCreateRequest = (await (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json() as Array<{ method: string }>)
    .filter((entry) => entry.method !== "GET").length;
  expect(writesAfterCreateRequest).toBe(postsBefore);

  await openView(page, "Agents");
  await lifecycleRow.getByRole("button", { name: "Sessions", exact: true }).click();
  await expect(navButton(page, "Session log")).toHaveAttribute("aria-current", "page");
  await expect(sessionLog(page).getByRole("combobox").first()).toHaveValue("agent_a");
  await expect(sessionLog(page).getByRole("table")).toContainText("Lifecycle Agent");
});

test("renders the grouped console in Chinese when the saved language is zh-CN", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("agents-core-web.language", "zh-CN"));
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.locator(".app-sidebar .brand-product")).toHaveText("控制台");
  await expect(consoleNav(page)).toHaveCount(0);
  const navigation = consoleNav(page, "控制台导航");
  await expect(navigation).toBeVisible();
  expect(await sidebarStructure(page, "控制台导航")).toEqual(expectedChineseGroups);
  await expect(navigation.getByRole("button", { name: "总览", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "总览", exact: true })).toBeVisible();
  await expect(page.getByLabel("部署健康状况", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "需要处理", exact: true })).toBeVisible();

  await navigation.getByRole("button", { name: "会话日志", exact: true }).click();
  await expect(navigation.getByRole("button", { name: "会话日志", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { level: 1, name: "会话日志", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "会话日志", exact: true }).getByRole("group", { name: "状态", exact: true })).toBeVisible();
  await navigation.getByRole("button", { name: "节点", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "节点", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "刷新沙箱状态", exact: true })).toBeVisible();
});
