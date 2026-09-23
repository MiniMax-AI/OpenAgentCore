import { expect, test, type Page } from "@playwright/test";
const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
async function connectAdmin(page: Page) {
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  await page.getByLabel("Deployment admin key").fill("fixture-admin-key");
  await page.getByRole("button", { name: "Connect admin", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toBeVisible();
}
test.beforeEach(async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/reset`);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toBeVisible();
});
test("admin access, node health, guarded removal and enrollment remain separate from project credentials", async ({ page, request }) => {
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  expect((await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls).toHaveLength(0);
  await page.getByLabel("Deployment admin key").fill("project-key");
  await page.getByRole("button", { name: "Connect admin", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("deployment admin key is required");
  await page.getByRole("button", { name: "Disconnect admin" }).click();
  await connectAdmin(page);
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
  await expect(page.getByLabel("Deployment admin key")).toHaveValue("");
  await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
});
test("microsandbox shares the manager and mobile tables stay contained", async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/sandbox-microsandbox`);
  await page.setViewportSize({ width: 390, height: 844 });
  await connectAdmin(page);
  await expect(page.locator(".sandbox-summary")).toContainText("microsandbox");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const table = page.getByRole("region", { name: "Sandbox nodes", exact: true });
  await expect(table).toBeVisible();
  const bounds = await table.boundingBox();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await expect(page.getByRole("button", { name: "Disconnect admin" })).toBeInViewport();
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
  await connectAdmin(page);
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
  await connectAdmin(page);
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
  await connectAdmin(page);
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).toContainText("No reported issue");
  await expect(page.getByRole("region", { name: "Sandbox allocations", exact: true })).not.toContainText("Node disconnected");
});
test("a missing resource preserves ownership and offers inspection without replacement", async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/sandbox-diagnostic?value=resource_missing`);
  await connectAdmin(page);
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
