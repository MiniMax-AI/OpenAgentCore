import { expect, test, type Page } from "@playwright/test";
const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
async function openManager(page: Page) {
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
}
test.beforeEach(async ({ page, request }) => {
  await page.route("**/console/config", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ sandbox_admin: true, node_installer: false }) }));
  await request.post(`${fixture}/__fixture/reset`);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toBeVisible();
});
test("console access needs no browser admin credential; removal and enrollment are guarded", async ({ page, request }) => {
  await openManager(page);
  await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Connect admin|Disconnect admin/ })).toHaveCount(0);
  const calls = (await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls;
  expect(calls.length).toBeGreaterThan(0);
  expect(calls.every((call: { authorization: unknown }) => call.authorization === null)).toBe(true);
  await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Provider ready");
  await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Host metrics unavailable");
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).toContainText("session_snapshot");
  await page.getByRole("button", { name: "Remove Core server", exact: true }).click();
  await page.getByRole("button", { name: "Confirm removal" }).click();
  await expect(page.getByRole("alert")).toContainText("active allocations or retained resources");
  await expect(page.getByRole("button", { name: "Remove Core server", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel removal" }).click();
  await page.getByRole("button", { name: "Remove Offline host", exact: true }).click();
  await page.getByRole("button", { name: "Confirm removal" }).click();
  await expect(page.getByRole("button", { name: "Remove Offline host", exact: true })).toHaveCount(0);
  await page.getByLabel("Core URL reachable from the node").fill("https://core.example");
  await page.getByRole("button", { name: "Generate enrollment command" }).click();
  await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/fixture-once-token/);
  await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/--enrollment-token-file/);
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(storage).not.toContain("fixture-admin-key"); expect(storage).not.toContain("fixture-once-token");
  expect(page.url()).not.toContain("fixture-admin-key");
  await page.reload();
  await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
});
test("microsandbox shares the manager and mobile tables stay contained", async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/sandbox-microsandbox`);
  await page.setViewportSize({ width: 390, height: 844 });
  await openManager(page);
  await expect(page.locator(".sandbox-summary")).toContainText("microsandbox");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const table = page.getByRole("region", { name: "Sandbox nodes", exact: true });
  await expect(table).toBeVisible();
  const bounds = await table.boundingBox();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await expect(page.getByLabel("Language / 语言")).toBeVisible();
  await page.getByLabel("Core URL reachable from the node").scrollIntoViewIfNeeded();
  await expect(page.getByLabel("Core URL reachable from the node")).toBeInViewport();
});
test("hosted creation defaults to automatic and an explicit unavailable node is never replaced", async ({ page }) => {
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.getByRole("button", { name: "New Session", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session" });
  await dialog.getByLabel("Saved Agent", { exact: true }).selectOption("agent_b");
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
  const advanced = dialog.getByRole("button", { name: /Advanced settings/ });
  if (await advanced.getAttribute("aria-expanded") !== "true") await advanced.click();
  await expect(dialog.getByLabel("Sandbox node", { exact: true })).toHaveValue("");
  await dialog.getByLabel("Sandbox node", { exact: true }).selectOption("node-local");
  const creates: Array<Record<string, unknown>> = [];
  await page.route("**/v1/agents/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    creates.push(route.request().postDataJSON());
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "runtime_node_unavailable", message: "Selected sandbox node is unavailable.", type: "server_error" } }) });
  });
  await dialog.getByRole("button", { name: "Create Session", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("Selected sandbox node is unavailable");
  await expect(dialog.getByLabel("Sandbox node", { exact: true })).toHaveValue("node-local");
  expect(creates).toHaveLength(1);
  expect(creates[0]?.x_agents_core).toEqual({ sandbox_node_id: "node-local" });
});
test("Session details show the actual Core placement", async ({ page }) => {
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Core server");
  await expect(dialog).toContainText("node-local");
});
test("empty nodes and a failed refresh have distinct states", async ({ page }) => {
  await page.route("**/core/v1/sandbox/nodes", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await openManager(page);
  await expect(page.getByText("No nodes registered. Add a node to provide hosted capacity.")).toBeVisible();
  await expect(page.getByText("No sandbox allocations.")).toBeVisible();
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "Deployment unavailable." } }) }));
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("alert")).toContainText("Previously loaded state is shown below");
});
test("late placement reads cannot replace another Session's placement", async ({ page, request }) => {
  const second = await request.post(`${fixture}/v1/agents/sessions`, {
    headers: { "OpenAI-Beta": "agents=v1", "Idempotency-Key": "placement-second" },
    data: { agent_id: "agent_b", environment: { type: "none" }, input: "Read placement", metadata: { title: "Placement second" }, stream: false },
  });
  expect(second.status()).toBe(201);
  const secondId = (await second.json()).id;
  let releaseOld: () => void = () => {};
  const oldReleased = new Promise<void>((resolve) => { releaseOld = resolve; });
  let oldRequested = false;
  await page.route("**/v1/agents/sessions/*/sandbox-placement", async (route) => {
    const isSecond = route.request().url().includes(secondId);
    if (!isSecond) { oldRequested = true; await oldReleased; }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ node_id: isSecond ? "new-node" : "old-node", node_name: isSecond ? "Second placement" : "Old placement", available: true, state: "active", compute_phase: "running" }) }).catch(() => {});
  });
  await page.reload();
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.locator('.session-row-action[aria-label="Manage Lifecycle Agent"]').click();
  await expect.poll(() => oldRequested).toBe(true);
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
  await page.locator('.session-row-action[aria-label="Manage Placement second"]').click();
  await expect(page.getByRole("dialog")).toContainText("Second placement");
  releaseOld();
  await expect(page.getByRole("dialog")).not.toContainText("Old placement");
});
test("disconnect diagnostics clear after reconnection in manager and Session details", async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=node_unavailable`);
  await openManager(page);
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).toContainText("Node disconnected");
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).toContainText("Existing resources stay assigned");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.locator(".conversation-session-action").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Node disconnected");
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=`);
  await dialog.getByRole("button", { name: "Refresh placement" }).click();
  await expect(dialog).toContainText("Available · Recorded allocation");
  await expect(dialog).not.toContainText("Node disconnected");
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
  await openManager(page);
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).toContainText("No reported issue");
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).not.toContainText("Node disconnected");
});
test("a missing resource preserves ownership and offers inspection without replacement", async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=resource_missing`);
  await openManager(page);
  const allocations = page.getByRole("region", { name: "Sandbox allocations", exact: true });
  await expect(allocations).toContainText("Sandbox resource missing");
  await expect(allocations).toContainText("retains the ownership record");
  await expect(allocations).toContainText("does not create a replacement automatically");
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await page.locator(".conversation-session-action").click();
  await expect(page.getByRole("dialog")).toContainText("Sandbox resource missing");
  await expect(page.getByRole("dialog")).toContainText("Check the provider resource on the assigned node");
  const requests = await (await request.get(`${fixture}/__fixture/requests`)).json();
  expect(requests.filter((entry: { method: string; path: string }) => entry.method === "POST" && entry.path === "/v1/agents/sessions")).toHaveLength(0);
});

test("Chinese defaults from browser preference, persists, and translates manager actions and diagnostics", async ({ page, request }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "languages", { get: () => ["zh-CN", "en-US"] }));
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=resource_missing`);
  await page.reload();
  await page.getByRole("button", { name: "托管沙箱管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "节点", exact: true })).toBeVisible();
  const allocations = page.getByRole("region", { name: "沙箱资源分配", exact: true });
  await expect(allocations).toContainText("沙箱资源缺失");
  await expect(allocations).toContainText("活跃");
  await expect(allocations).toContainText("运行中");
  await expect(page.getByRole("region", { name: "沙箱节点", exact: true })).toContainText("主机指标不可用（心跳已过期）");
  await page.getByRole("button", { name: "移除 Core server", exact: true }).click();
  await page.getByRole("button", { name: "确认移除", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("节点仍有活跃分配或保留资源");
  await page.getByRole("button", { name: "取消移除", exact: true }).click();
  await page.getByRole("button", { name: "刷新沙箱状态" }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByLabel("节点可访问的 Core 地址").fill("https://core.example");
  await page.getByRole("button", { name: "生成注册命令", exact: true }).click();
  await expect(page.getByLabel("一次性注册命令")).toHaveValue(/fixture-once-token/);
  await page.getByLabel("Language / 语言").selectOption("en");
  await expect(page.getByRole("heading", { name: "Hosted Sandbox Manager" })).toBeVisible();
  await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/fixture-once-token/);
  await page.reload();
  await expect(page.getByLabel("Language / 语言")).toHaveValue("en");
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
  await page.getByLabel("Language / 语言").selectOption("zh");
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "sandbox_admin_not_configured", message: "Sandbox administration is not configured on this console" } }) }));
  await page.getByRole("button", { name: "刷新沙箱状态" }).click();
  await expect(page.getByRole("alert")).toContainText("此控制台尚未配置沙箱管理权限");
});

test("a direct Core connection never requests sandbox administration and clears enrollment", async ({ page, request }) => {
  await openManager(page);
  await page.getByLabel("Core URL reachable from the node").fill("https://core.example");
  await page.getByRole("button", { name: "Generate enrollment command" }).click();
  await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/fixture-once-token/);
  const before = (await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls.length;
  await page.getByRole("button", { name: "Configure Agent Core connection", exact: true }).click();
  const connection = page.getByRole("dialog", { name: "Connect an Agent Core", exact: true });
  await connection.getByRole("radio", { name: /Other compatible Core/ }).check();
  await connection.getByLabel("Compatible Core base URL").fill(`${new URL(page.url()).origin}/v1`);
  await connection.getByLabel("Bearer token").fill("project-only");
  await connection.getByRole("button", { name: "Apply connection", exact: true }).click();
  await expect(page.getByText("Sandbox management is available through the signed-in console connection.", { exact: false })).toBeVisible();
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toHaveCount(0);
  expect((await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls).toHaveLength(before);
});

test("an uncertain enrollment write is not retried and a late response cannot survive navigation", async ({ page }) => {
  await openManager(page);
  let attempts = 0;
  let release: () => void = () => {};
  const pending = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/core/v1/sandbox/enrollment-tokens", async (route) => {
    attempts += 1;
    await pending;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ token: "stale-token", expires_at: "2026-09-24T00:00:00Z" }) }).catch(() => {});
  });
  await page.getByLabel("Core URL reachable from the node").fill("https://core.example");
  await page.getByRole("button", { name: "Generate enrollment command" }).click();
  await expect.poll(() => attempts).toBe(1);
  await page.getByRole("button", { name: "Sessions", exact: true }).click();
  await openManager(page);
  release();
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
  await expect(page.getByLabel("Core URL reachable from the node")).toHaveValue(new URL(page.url()).origin);
  expect(attempts).toBe(1);
});
