import { expect, test, type Page } from "@playwright/test";
const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const installer = { sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) };
async function openManager(page: Page) {
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
}
async function details(page: Page, name = "Core server") {
  await page.locator(".sandbox-topology-node").filter({ hasText: name }).click();
  const card = page.locator(".sandbox-node-card").filter({ has: page.getByRole("heading", { name, exact: true }) });
  return card;
}
test.beforeEach(async ({ page, request }) => {
  await page.route("**/console/config", (route) => route.fulfill({ json: installer }));
  await request.post(`${fixture}/__fixture/reset`);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toBeVisible();
});

test("nodes lead the page, details preserve diagnostics and removal is confirmed", async ({ page, request }) => {
  await openManager(page);
  await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
  await expect(page.getByLabel("Core URL reachable from the node")).toHaveCount(0);
  await expect(page.getByText("fixture-installation", { exact: true })).toBeHidden();
  await expect(page.getByText("session_snapshot", { exact: true })).toBeHidden();
  const nodes = page.getByRole("region", { name: "Sandbox nodes", exact: true });
  await expect(nodes).toContainText("Available");
  await expect(nodes).toContainText("Offline");
  const card = await details(page);
  await expect(card.getByRole("region", { name: "Sandbox allocations" })).toContainText("session_snapshot");
  await card.getByRole("button", { name: "Remove Core server", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await card.getByRole("button", { name: "Confirm removal" }).click();
  await expect(page.getByRole("alert")).toContainText("active allocations or retained resources");
  await card.getByRole("button", { name: "Cancel removal" }).click();
  const offline = await details(page, "Offline host");
  await offline.getByRole("button", { name: "Remove Offline host" }).click();
  await offline.getByRole("button", { name: "Confirm removal" }).click();
  await expect(offline).toHaveCount(0);
  const calls = (await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls;
  expect(calls.every((call: { authorization: unknown }) => call.authorization === null)).toBe(true);
});

test("add opens one command, copy works, existing hosts do not imply connection, and close discards secrets", async ({ page, context, request }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openManager(page);
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Add node" });
  const command = dialog.getByLabel("One-time enrollment command");
  await expect(command).toHaveValue(/fixture-once-token/);
  await expect(dialog.getByRole("status")).toHaveText("Waiting for your node to connect…");
  await expect(dialog.getByRole("textbox")).toHaveCount(1);
  await dialog.getByRole("button", { name: "Copy node command" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await command.inputValue());
  await expect(dialog).toContainText("One-time enrollment token expires");
  await request.post(`${fixture}/__fixture/sandbox-add-node`);
  await expect(dialog.getByRole("status")).toContainText("Enrolled host · Connected", { timeout: 10000 });
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain("fixture-once-token");
  await page.keyboard.press("Escape");
  await expect(command).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeFocused();
  const calls = (await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls;
  expect(calls.filter((call: { method: string; path: string }) => call.method === "POST" && call.path.endsWith("enrollment-tokens"))).toHaveLength(1);
});

test("unavailable installer shows compact guidance and never creates an enrollment", async ({ page, request }) => {
  await page.route("**/console/config", (route) => route.fulfill({ json: { sandbox_admin: true, node_installer: false } }));
  await openManager(page);
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Node installation is unavailable");
  await expect(dialog.getByRole("textbox")).toHaveCount(0);
  expect((await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls.filter((call: { method: string }) => call.method === "POST")).toHaveLength(0);
});

test("empty nodes and stale reads are distinct; failed reads cannot imply ready", async ({ page }) => {
  await openManager(page);
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ status: 503, json: { error: { message: "Deployment unavailable." } } }));
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("alert")).toContainText("Previously loaded state is shown below");
  await expect(page.locator(".sandbox-topology-node-status").first()).toHaveText("Status unconfirmed");
  await page.unroute("**/core/v1/sandbox/deployment");
  await page.route("**/core/v1/sandbox/nodes", (route) => route.fulfill({ json: { data: [] } }));
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("heading", { name: "Add your first node" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Sandbox allocations" })).toHaveCount(0);
});

for (const value of ["node_unavailable", "resource_missing"]) {
  test(`${value} remains visible in node details and clears after recovery`, async ({ page, request }) => {
    await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=${value}`);
    await openManager(page);
    const card = await details(page);
    const allocations = card.getByRole("region", { name: "Sandbox allocations" });
    await expect(allocations).toContainText(value === "node_unavailable" ? "Node disconnected" : "Sandbox resource missing");
    await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=`);
    await page.getByRole("button", { name: "Refresh sandbox state" }).click();
    await expect(allocations).toContainText("No reported issue");
  });
}

test("Chinese actions, diagnostics, and enrollment are translated and language persists", async ({ page, request }) => {
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=resource_missing`);
  await page.getByRole("button", { name: "托管沙箱管理", exact: true }).click();
  await page.locator(".sandbox-topology-node").first().click();
  await expect(page.getByRole("region", { name: "沙箱资源分配" }).first()).toContainText("沙箱资源缺失");
  await page.getByRole("button", { name: "添加节点", exact: true }).click();
  await expect(page.getByLabel("一次性注册命令")).toHaveValue(/fixture-once-token/);
  await expect(page.getByRole("dialog")).toContainText("等待节点连接");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "语言和外观" }).click();
  await page.getByRole("menuitemradio", { name: "English" }).click();
  await page.reload();
  await expect(page.getByRole("button", { name: "Language and appearance" })).toContainText("EN");
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
});

test("an uncertain write is never retried; closing discards a late token and allows a fresh request", async ({ page }) => {
  await openManager(page);
  let attempts = 0;
  let release: () => void = () => {};
  const pending = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/core/v1/sandbox/enrollment-tokens", async (route) => {
    attempts++;
    if (attempts === 1) await pending;
    await route.fulfill({ json: { token: attempts === 1 ? "stale-token" : "fresh-token", expires_at: new Date(Date.now() + 600000).toISOString() } }).catch(() => {});
  });
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  await expect.poll(() => attempts).toBe(1);
  await page.keyboard.press("Escape");
  release();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(attempts).toBe(1);
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/fresh-token/);
  expect(attempts).toBe(2);
});

test("a connected node remains successful after its enrollment token expires", async ({ page, request }) => {
  const startedAt = new Date();
  await page.clock.install({ time: startedAt });
  let attempts = 0;
  await page.route("**/core/v1/sandbox/enrollment-tokens", (route) => {
    attempts++;
    return route.fulfill({ json: { token: "short-lived-token", expires_at: new Date(startedAt.getTime() + 60000).toISOString() } });
  });
  await openManager(page);
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Add node" });
  await expect(dialog.getByLabel("One-time enrollment command")).toHaveValue(/short-lived-token/);
  await request.post(`${fixture}/__fixture/sandbox-add-node`);
  await page.clock.fastForward(3000);
  await expect(dialog.getByRole("status")).toHaveText("Enrolled host · Connected");
  await expect(dialog.getByLabel("One-time enrollment command")).toHaveCount(0);
  await page.clock.fastForward(65000);
  await expect(dialog.getByRole("status")).toHaveText("Enrolled host · Connected");
  await expect(dialog.getByText("Generate a new command to continue.", { exact: true })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Generate new command" })).toHaveCount(0);
  expect(attempts).toBe(1);
  await dialog.getByRole("button", { name: "Done", exact: true }).click();
  await expect(dialog).toBeHidden();
});

test("expired commands and failed writes require an explicit retry", async ({ page }) => {
  await openManager(page);
  let attempts = 0;
  await page.route("**/core/v1/sandbox/enrollment-tokens", (route) => {
    attempts++;
    return attempts === 1 ? route.fulfill({ json: { token: "expired-token", expires_at: "2020-01-01T00:00:00Z" } }) : route.fulfill({ status: 503, json: { error: { message: "Unavailable" } } });
  });
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("Command expired");
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
  expect(attempts).toBe(1);
  await page.getByRole("button", { name: "Generate new command" }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toBeVisible();
  expect(attempts).toBe(2);
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
});

test("topology supports keyboard inspection, distinct health states, and reduced motion", async ({ page }, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.route("**/core/v1/sandbox/nodes", async (route) => {
    const result = await (await route.fetch()).json();
    result.data.push({ ...result.data[0], id: "node-provider-down", name: "Provider unavailable host", provider_ready: false });
    for (let index = 0; index < 4; index++) result.data.push({ ...result.data[0], id: `extra-${index}`, name: `Extra host ${index}` });
    await route.fulfill({ json: result });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await openManager(page);
  const nodes = page.locator(".sandbox-topology-node");
  await expect(nodes).toHaveCount(7);
  await expect(page.locator(".sandbox-topology-node.warning")).toContainText("Unavailable");
  await expect(page.locator(".sandbox-topology-connection.offline .sandbox-topology-flow")).toHaveCount(0);
  await expect(page.locator(".sandbox-topology-flow").first()).toHaveCSS("animation-name", "none");
  const boxes = await nodes.evaluateAll((elements) => elements.map((element) => {
    const { x, y, width, height } = element.getBoundingClientRect(); return { x, y, width, height };
  }));
  for (let a = 0; a < boxes.length; a++) for (let b = a + 1; b < boxes.length; b++) {
    const first = boxes[a]!, second = boxes[b]!;
    expect(first.x + first.width <= second.x || second.x + second.width <= first.x || first.y + first.height <= second.y || second.y + second.height <= first.y).toBe(true);
  }
  await page.locator(".sandbox-topology").screenshot({ path: testInfo.outputPath("topology-seven-nodes.png"), animations: "disabled" });
  await nodes.first().focus();
  await page.keyboard.press("Enter");
  await expect(nodes.first()).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#sandbox-selected-node")).toContainText("session_snapshot");
  await expect(page.getByRole("button", { name: "Remove Core server" })).toBeVisible();
  await page.locator("#sandbox-selected-node").screenshot({ path: testInfo.outputPath("selected-node-details.png"), animations: "disabled" });
});

for (const width of [1280, 1440]) {
  for (const theme of ["light", "dark"]) {
    test(`node manager and add modal fit ${width}px ${theme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
      await openManager(page);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ animations: "disabled", path: testInfo.outputPath(`nodes-${width}-${theme}.png`) });
      await page.getByRole("button", { name: "Add node", exact: true }).click();
      await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/fixture-once-token/);
      const copyButton = page.getByRole("button", { name: "Copy node command" });
      await expect(copyButton).toBeInViewport();
      const copyBounds = await copyButton.boundingBox();
      const commandBounds = await page.locator(".sandbox-command").boundingBox();
      expect(copyBounds!.x + copyBounds!.width).toBeLessThanOrEqual(commandBounds!.x + commandBounds!.width);
      const bounds = await page.getByRole("dialog").boundingBox();
      expect(bounds!.x).toBeGreaterThanOrEqual(0);
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
      await page.screenshot({ animations: "disabled", path: testInfo.outputPath(`add-node-${width}-${theme}.png`) });
    });
  }
}
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

for (const theme of ["light", "dark"]) {
  test(`Chinese topology and node details render on desktop in ${theme}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
    await page.getByRole("button", { name: "Language and appearance" }).click();
    await page.getByRole("menuitemradio", { name: "简体中文" }).click();
    await page.getByRole("button", { name: "托管沙箱管理", exact: true }).click();
    await expect(page.getByRole("region", { name: "沙箱节点", exact: true })).toContainText("可用");
    await page.screenshot({ path: testInfo.outputPath(`topology-zh-${theme}.png`), animations: "disabled" });
    await page.locator(".sandbox-topology-node").first().click();
    await expect(page.getByRole("button", { name: "移除 Core server", exact: true })).toBeVisible();
    await page.locator("#sandbox-selected-node").screenshot({ path: testInfo.outputPath(`node-details-zh-${theme}.png`), animations: "disabled" });
  });
}
