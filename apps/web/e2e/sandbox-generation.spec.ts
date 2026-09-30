import { expect, test, type Locator, type Page, type TestInfo } from "@playwright/test";
import type { SandboxAllocation, SandboxDeployment, SandboxNode } from "@oac/agents-client";

import { expectManagementBoundary, failNext, openConsole, setDeployment, setNode, writes } from "./console";

const deploymentPath = "/core/v1/sandbox/deployment";
const templateDiscoveryPath = "/core/v1/sandbox/providers/e2b/discovery";
const rollout = (page: Page) => page.getByRole("region", { name: "Configuration rollout", exact: true });
const fact = (scope: Locator, label: string) => scope.locator("dt").filter({ hasText: new RegExp(`^${label}`) }).locator("..").locator("dd");
async function inspectRollout(page: Page, values: Record<string, string>) {
  await rollout(page).getByRole("button", { name: "View rollout details", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Configuration rollout", exact: true });
  for (const [label, value] of Object.entries(values)) await expect(fact(dialog, label)).toHaveText(value);
  await dialog.getByRole("button", { name: "Close dialog", exact: true }).click();
  await expect(dialog).toBeHidden();
}
const deploymentRead = (page: Page) => read<SandboxDeployment>(page, deploymentPath);
async function read<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(new URL(path, page.url()).href);
  expect(response.ok()).toBe(true);
  return response.json() as Promise<T>;
}
async function inventory(page: Page) {
  const nodes = (await read<{ data: SandboxNode[] }>(page, "/core/v1/sandbox/nodes")).data;
  const allocations = (await Promise.all(nodes.map((node) => read<{ data: SandboxAllocation[] }>(page, `/core/v1/sandbox/nodes/${node.id}/allocations`)))).flatMap((result) => result.data);
  return { nodes, allocations };
}
async function saveConfiguration(page: Page) {
  const sent = page.waitForRequest((request) => request.method() === "PUT" && request.url().endsWith(deploymentPath));
  const response = page.waitForResponse((response) => response.request().method() === "PUT" && response.url().endsWith(deploymentPath));
  await page.getByRole("button", { name: "Save configuration", exact: true }).click();
  return { input: (await sent).postDataJSON(), response: await response };
}
async function editE2B(page: Page, key?: string) {
  await page.getByRole("button", { name: "Change resources", exact: true }).click();
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  if (key !== undefined) await page.getByLabel("E2B API key", { exact: true }).fill(key);
  await page.getByRole("button", { name: "Next", exact: true }).click();
}

async function capture(page: Page, info: TestInfo, name: string, scope?: Locator) {
  const path = info.outputPath(`${name}.png`);
  if (scope) await scope.screenshot({ path });
  else await page.screenshot({ path, fullPage: true });
  await info.attach(name, { path, contentType: "image/png" });
}

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("online configuration retains existing resources and stops rapid polling when preparation settles", async ({ page, request }, info) => {
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "light" });
  const pending = new Set<unknown>();
  page.on("request", (sent) => { if (sent.method() === "GET" && sent.url().endsWith(deploymentPath)) pending.add(sent); });
  page.on("requestfinished", (sent) => pending.delete(sent));
  page.on("requestfailed", (sent) => pending.delete(sent));
  await openConsole(page, request, "system?id=sandbox");
  await expect(rollout(page)).toBeVisible();
  const before = await inventory(page);
  expect(before.allocations.length).toBeGreaterThan(0);
  await setDeployment(request, { resources: { allocations: before.allocations.length, pending: 0 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await page.getByRole("button", { name: "Change resources", exact: true }).click();
  await page.getByRole("button", { name: /^Large/ }).click();
  const saved = await saveConfiguration(page);
  expect(saved.response.ok()).toBe(true);
  expect(saved.input).toMatchObject({ provider: "docker", expected_generation: 1 });
  const current = await saved.response.json() as SandboxDeployment;
  expect(current.generation).toBe(2);
  expect(current.resources.allocations).toBe(before.allocations.length);
  await inspectRollout(page, { "Core preparation": "Preparing configuration", "Previous-generation sandboxes": String(before.allocations.length) });
  const after = await inventory(page);
  expect(after.nodes.map((node) => node.id)).toEqual(before.nodes.map((node) => node.id));
  expect(after.allocations).toEqual(before.allocations);
  expect(after.nodes.map((node) => node.rollout.ready_generation)).toEqual(before.nodes.map((node) => node.rollout.ready_generation));

  // Explicit Core observations settle preparation; old Session ownership remains.
  await setNode(request, { id: "node-local", rollout: { state: "ready", ready_generation: 2 } });
  await setNode(request, { id: "node-gpu", rollout: { state: "failed", ready_generation: 1, diagnostic: "runtime_image_unavailable" } });
  await setNode(request, { id: "node-edge", rollout: { state: "unknown", ready_generation: 1 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(rollout(page)).toContainText("Needs attention");
  await inspectRollout(page, { "Core preparation": "No active preparation", "Previous-generation sandboxes": String(before.allocations.length), "Preparation failed": "1", "Target readiness unknown": "1" });
  await expect(page.getByRole("button", { name: "Change resources", exact: true })).toBeEnabled();
  // Watch longer than the five-second preparation interval. Retained resources
  // are not a reason to keep that interval alive; ordinary thirty-second reads remain.
  await expect.poll(() => pending.size).toBe(0);
  const rapidRead = await page.waitForRequest((sent) => sent.method() === "GET" && sent.url().endsWith(deploymentPath), { timeout: 6500 }).then(() => true, () => false);
  expect(rapidRead).toBe(false);
  await inspectRollout(page, { "Target generation": "2" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await capture(page, info, "generation-settled-en-light-1280");
  await page.emulateMedia({ colorScheme: "dark" });
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  const localized = page.getByRole("region", { name: "配置更新进度", exact: true });
  await expect(localized).toContainText("需要检查");
  await localized.getByRole("button", { name: "查看详情", exact: true }).click();
  const details = page.getByRole("dialog", { name: "配置更新进度", exact: true });
  await expect(fact(details, "Core 准备状态")).toHaveText("没有正在进行的准备任务");
  await expect(fact(details, "目标代次")).toHaveText("2");
  await expect(fact(details, "旧代次沙箱")).toHaveText(String(before.allocations.length));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await capture(page, info, "generation-settled-zh-dark-1280");
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
});

test("unknown target preparation preserves live old-generation service while an offline pin stays offline", async ({ page, request }, info) => {
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await openConsole(page, request, "system?id=sandbox", { fresh: true });
  await setDeployment(request, { generation: 2 });
  await setNode(request, { id: "node-local", online: true, provider_ready: true, rollout: { state: "unknown", ready_generation: 1 } });
  await setNode(request, { id: "node-edge", online: false, provider_ready: true, rollout: { state: "unknown", ready_generation: 1 } });
  await setNode(request, { id: "node-gpu", rollout: { state: "update_required", ready_generation: 1 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await inspectRollout(page, { "Node software incompatible": "1" });
  await page.getByRole("button", { name: "Nodes", exact: true }).click();
  const serving = page.getByRole("row", { name: /core-01/ });
  await expect(serving).toContainText("Available");
  await expect(serving).toContainText("Target readiness unknown");
  const row = page.getByRole("row", { name: /edge-03/ });
  await expect(row).toContainText("Offline");
  await expect(row).not.toContainText("Ready for target");
  await page.getByRole("button", { name: "Open edge-03", exact: true }).click();
  await expect(page.getByRole("heading", { name: "edge-03", exact: true })).toBeVisible();
  const facts = page.locator(".resource-facts");
  await expect(facts.locator("dt").filter({ hasText: /^Serving generation/ }).locator("..").locator("dd")).toHaveText("1");
  await expect(facts.locator("dt").filter({ hasText: /^Target preparation/ }).locator("..").locator("dd")).toContainText("Target readiness unknown");
  await expect(facts.locator("dt").filter({ hasText: /^Status$/ }).locator("..")).toContainText("Offline");
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  const setup = page.getByRole("region", { name: "Getting started" }).getByRole("listitem").filter({ hasText: "Get sandboxes ready" });
  await expect(setup).toContainText("Done");
  await expect(rollout(page)).toHaveCount(0);
  await page.getByRole("button", { name: "System", exact: true }).click();
  await expect(rollout(page)).toHaveCount(0);
  await page.getByRole("button", { name: "Manage sandbox configuration", exact: true }).click();
  await inspectRollout(page, { "Target generation": "2", "Target readiness unknown": "2", "Node software incompatible": "1" });
  await capture(page, info, "generation-configuration-summary-en", rollout(page));
  expect(await writes(request)).toEqual([]);
});

test("a generation-only change preserves mixed allocation ownership through failed inventory reads", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(rollout(page)).toBeVisible();
  let expected: { session: string; generation: string }[] = [];
  await page.route("**/core/v1/sandbox/nodes/node-local/allocations", async (route) => {
    const response = await route.fetch();
    const body = await response.json() as { data: SandboxAllocation[] };
    body.data = body.data.map((allocation, index) => ({ ...allocation, deployment_generation: index % 2 + 1 }));
    expected = body.data.map((allocation) => ({ session: allocation.session_id, generation: String(allocation.deployment_generation) }));
    await route.fulfill({ response, json: body });
  });
  await setDeployment(request, { generation: 2 });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await inspectRollout(page, { "Target generation": "2" });
  await page.getByRole("button", { name: "Nodes", exact: true }).click();
  await page.getByRole("button", { name: "Open core-01", exact: true }).click();
  const table = page.getByRole("table", { name: "Sandbox allocations", exact: true });
  await expect(table.getByRole("columnheader", { name: "Configuration generation", exact: true })).toBeVisible();
  await expect.poll(() => [...new Set(expected.map((entry) => entry.generation))].sort()).toEqual(["1", "2"]);
  const ownership = () => table.locator("tbody tr").evaluateAll((rows) => rows.map((row) => ({
    session: row.querySelector("th code")?.getAttribute("title"),
    generation: row.querySelector("td")?.textContent,
  })));
  await expect.poll(ownership).toEqual(expected);
  await page.route("**/core/v1/sandbox/nodes", (route) => route.fulfill({ status: 503, json: { error: { type: "server_error", code: null, message: "Node inventory unavailable.", param: null } } }));
  await setDeployment(request, { generation: 3 });
  const refreshed = page.waitForResponse((response) => response.request().method() === "GET" && response.url().endsWith(deploymentPath));
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  expect((await (await refreshed).json()).generation).toBe(3);
  await expect(page.getByText("Node state could not be read", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "core-01", exact: true })).toBeVisible();
  await expect.poll(ownership).toEqual(expected);
  expect(await writes(request)).toEqual([]);
});

test("E2B omitted-key updates keep the saved key while explicit same-key replacements each advance generation", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "e2b" });
  await expect(rollout(page)).toBeVisible();
  const initial = await deploymentRead(page);
  await editE2B(page);
  const omitted = await saveConfiguration(page);
  expect(omitted.input).toMatchObject({ provider: "e2b", expected_generation: 1, configuration: { template: initial.configuration!.template } });
  expect(omitted.input).not.toHaveProperty("credential");
  expect((await omitted.response.json()).generation).toBe(1);
  await expect(page.getByRole("dialog", { name: "Change resources", exact: true })).toBeHidden();
  const key = "fixture-same-team-key";
  for (const generation of [1, 2]) {
    await editE2B(page, key);
    const explicit = await saveConfiguration(page);
    expect(explicit.input).toMatchObject({ provider: "e2b", expected_generation: generation, configuration: { template: initial.configuration!.template } , credential: { api_key: key } });
    const current = await explicit.response.json() as SandboxDeployment;
    expect(current.generation).toBe(generation + 1);
    expect(current.resources).toEqual(initial.resources);
    await inspectRollout(page, { "Target generation": String(generation + 1) });
  }
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(storage).not.toContain(key);
  expect(await page.content()).not.toContain(key);
  expect(await writes(request)).toEqual([
    `PUT ${deploymentPath}`,
    `POST ${templateDiscoveryPath}`,
    `PUT ${deploymentPath}`,
    `POST ${templateDiscoveryPath}`,
    `PUT ${deploymentPath}`,
  ]);
});

test("a different E2B team leaves the committed configuration intact and requires a deliberate reset", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox", { sandbox: "e2b" });
  await expect(rollout(page)).toBeVisible();
  const before = await deploymentRead(page);
  await editE2B(page, "fixture-other-team-key");
  const rejected = await saveConfiguration(page);
  expect(rejected.response.status()).toBe(409);
  await expect(page.locator(".wizard-rejection")).toContainText("Reset before changing teams.");
  await expect(page.locator(".wizard-rejection")).toContainText("the saved configuration is unchanged");
  expect(await deploymentRead(page)).toEqual(before);
  await expect(page.getByRole("button", { name: "Save configuration", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Keep saved key", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Change resources", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Couldn't confirm the sandbox change" })).toHaveCount(0);
  expect(await writes(request)).toEqual([`POST ${templateDiscoveryPath}`, `PUT ${deploymentPath}`]);
  await page.getByRole("button", { name: "Cancel editing", exact: true }).click();
  await page.getByRole("button", { name: "Reset deployment", exact: true }).click();
  const reset = page.getByRole("dialog", { name: "Reset sandbox deployment?", exact: true });
  await expect(reset).toContainText("Hosted Sessions will be archived permanently.");
  expect(await writes(request)).toEqual([`POST ${templateDiscoveryPath}`, `PUT ${deploymentPath}`]);
  await reset.getByRole("button", { name: "Back", exact: true }).click();
  await editE2B(page);
  expect(await writes(request)).toEqual([`POST ${templateDiscoveryPath}`, `PUT ${deploymentPath}`]);
});


test("uncertain configuration refresh and inventory retry preserve a draft until its generation changes", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(rollout(page)).toBeVisible();
  const initial = await deploymentRead(page);
  const committedSize = await fact(page.getByRole("region", { name: "Deployment provider", exact: true }), "Each sandbox").first().innerText();
  await page.getByRole("button", { name: "Change resources", exact: true }).click();
  const edit = page.getByRole("dialog", { name: "Change resources", exact: true });
  await edit.getByRole("button", { name: /^Large/ }).click();
  const wizard = edit.getByRole("region", { name: "Change the sandbox configuration", exact: true });
  const draftSize = await fact(wizard, "Each sandbox").innerText();
  expect(draftSize).not.toBe(committedSize);
  await failNext(request, { method: "PUT", path: deploymentPath, status: 503, message: "Configuration outcome is unconfirmed." });
  const result = await saveConfiguration(page);
  expect(result.response.status()).toBe(503);
  expect(result.input.expected_generation).toBe(initial.generation);
  let failInventory = true;
  await page.route("**/core/v1/sandbox/nodes", (route) => failInventory
    ? route.fulfill({ status: 503, json: { error: { type: "server_error", code: null, message: "Node inventory unavailable.", param: null } } })
    : route.continue());
  await expect(edit).toContainText("The sandbox service is unavailable. Refresh to check the current state.");
  await expect(edit).toContainText("Refresh sandbox state to confirm whether the change was saved before submitting again.");
  const failedInventory = page.waitForResponse((response) => response.url().endsWith("/sandbox/nodes") && response.status() === 503);
  await edit.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await failedInventory;
  // Configuration stays authoritative even when the independent node inventory fails.
  await expect(wizard.getByRole("heading", { name: "Review and save", exact: true })).toBeVisible();
  await expect(fact(wizard, "Each sandbox")).toHaveText(draftSize);
  expect((await deploymentRead(page)).generation).toBe(initial.generation);
  await expect(wizard.getByRole("button", { name: "Save configuration", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
  failInventory = false;
  const inventoryRecovered = page.waitForResponse((response) => response.url().endsWith("/sandbox/nodes") && response.ok());
  await edit.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await inventoryRecovered;
  await expect(fact(wizard, "Each sandbox")).toHaveText(draftSize);
  await expect(wizard.getByRole("button", { name: "Save configuration", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
  await setDeployment(request, { generation: initial.generation + 1 });
  await edit.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(edit).toBeHidden();
  await inspectRollout(page, { "Target generation": String(initial.generation + 1) });
  await expect(wizard).toHaveCount(0);
  await expect(fact(page.getByRole("region", { name: "Deployment provider", exact: true }), "Each sandbox")).toHaveText(committedSize);
  await page.getByRole("button", { name: "Change resources", exact: true }).click();
  await expect(wizard.getByRole("button", { name: /^Large/ })).toBeVisible();
  await expect(wizard.getByRole("heading", { name: "Review and save", exact: true })).toHaveCount(0);
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
});


test("an observed installation change discards the old reset confirmation even at the same generation", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(rollout(page)).toBeVisible();
  // A genuine preparing projection enables normal five-second observation while
  // the confirmation is open; the test never clicks through a modal overlay.
  await setNode(request, { id: "node-local", rollout: { state: "preparing", ready_generation: 1 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await inspectRollout(page, { "Core preparation": "Preparing configuration" });
  await page.getByRole("button", { name: "Reset deployment", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Reset sandbox deployment?", exact: true });
  await expect(dialog).toBeVisible();
  const initial = await deploymentRead(page);
  await setDeployment(request, { installation_id: `${initial.installation_id}-replacement` });
  await expect(dialog).toHaveCount(0);
  await inspectRollout(page, { "Target generation": String(initial.generation) });
  expect(await writes(request)).toEqual([]);
  await page.getByRole("button", { name: "Reset deployment", exact: true }).click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Back", exact: true }).click();
  expect(await writes(request)).toEqual([]);
});


test("closing an uncertain edit preserves the shared write block until a successful read", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await page.getByRole("button", { name: "Change resources", exact: true }).click();
  const edit = page.getByRole("dialog", { name: "Change resources", exact: true });
  await edit.getByRole("button", { name: /^Large/ }).click();
  await failNext(request, { method: "PUT", path: deploymentPath, status: 503, message: "Configuration outcome is unconfirmed." });
  let failReads = true;
  await page.route("**/core/v1/sandbox/deployment", (route) => route.request().method() === "GET" && failReads
    ? route.fulfill({ status: 503, json: { error: { type: "server_error", code: null, message: "Deployment read unavailable.", param: null } } })
    : route.continue());
  const result = await saveConfiguration(page);
  expect(result.response.status()).toBe(503);
  await expect(edit).toContainText("The sandbox service is unavailable. Refresh to check the current state.");
  await edit.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(edit.getByRole("button", { name: "Save configuration", exact: true })).toBeDisabled();
  await edit.getByRole("button", { name: "Cancel editing", exact: true }).click();
  await expect(edit).toBeHidden();
  await expect(page.getByRole("button", { name: "Change resources", exact: true })).toBeDisabled();
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
  failReads = false;
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(page.getByRole("button", { name: "Change resources", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([`PUT ${deploymentPath}`]);
});
