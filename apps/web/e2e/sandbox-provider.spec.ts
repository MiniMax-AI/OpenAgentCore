import { expect, test, type Page } from "@playwright/test";
import type { SandboxDeployment } from "../../../packages/agents-client/src/sandbox-client";

const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const template = "runtime:00000000-0000-0000-0000-000000000001";
function deployment(overrides: Partial<SandboxDeployment> = {}): SandboxDeployment {
  return { installation_id: "installation", provider: "docker", core_url: "https://core.example", maintenance: false, owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 2, pending: 1 }, ...overrides };
}
async function openManager(page: Page) {
  await page.goto("/#sandbox");
  await expect(page.getByRole("heading", { name: "Hosted Sandbox Manager", exact: true })).toBeVisible();
}
async function e2bForm(page: Page) {
  await page.getByLabel("Where to run sandboxes").selectOption("direct");
  await page.getByLabel("E2B API key", { exact: true }).fill("fixture-private-e2b-key");
  await page.getByLabel("Immutable Runtime template", { exact: true }).fill(template);
}
test.beforeEach(async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/reset`);
  await page.route("**/console/config", (route) => route.fulfill({ json: { sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) } }));
});

test("E2B setup has no physical node or enrollment and never persists the API key", async ({ page }) => {
  let current = deployment({ provider: "", generation: 0, mode: "", resources: { allocations: 0, pending: 0 } });
  const writes: unknown[] = [];
  await page.route("**/core/v1/sandbox/deployment", (route) => {
    if (route.request().method() === "POST") {
      const input = route.request().postDataJSON(); writes.push(input);
      current = deployment({ provider: "e2b", generation: 1, mode: "direct", resources: { allocations: 0, pending: 0 }, e2b: { template: input.e2b.template, credential_configured: true } });
    }
    return route.fulfill({ json: current });
  });
  await openManager(page);
  await e2bForm(page);
  await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await expect(page.getByText("Saved configuration does not confirm execution readiness.", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Nodes", exact: true })).toHaveCount(0);
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveCount(0);
  expect(writes).toEqual([{ provider: "e2b", core_url: "https://core.example", e2b: { api_key: "fixture-private-e2b-key", template } }]);
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain("fixture-private-e2b-key");
  let nodeReads = 0;
  await page.route("**/core/v1/sandbox/nodes", (route) => { nodeReads++; return route.fulfill({ status: 500 }); });
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("button", { name: "Enter maintenance to change provider" })).toBeEnabled();
  expect(nodeReads).toBe(0);
});

test("switching waits for maintenance and verified cleanup, keeps Core origin, then requires explicit resume", async ({ page }) => {
  let current = deployment();
  const writes: Array<{ method: string; body: Record<string, unknown> }> = [];
  await page.route("**/core/v1/sandbox/deployment{,/maintenance}", async (route) => {
    const method = route.request().method();
    if (method !== "GET") {
      const body = route.request().postDataJSON(); writes.push({ method, body });
      expect(body.expected_generation).toBe(current.generation);
      if (method === "PATCH") current = { ...current, maintenance: body.maintenance };
      if (method === "PUT") {
        expect(current.maintenance).toBe(true);
        expect(current.resources).toEqual({ allocations: 0, pending: 0 });
        current = deployment({ provider: body.provider, generation: 2, mode: "direct", maintenance: true, resources: { allocations: 0, pending: 0 }, e2b: { template: body.e2b.template, credential_configured: true } });
      }
    }
    await route.fulfill({ json: current });
  });
  await openManager(page);
  await expect(page.getByRole("button", { name: "Change provider", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Enter maintenance to change provider" }).click();
  await expect(page.getByRole("button", { name: "Change provider", exact: true })).toBeDisabled();
  await expect(page.getByRole("link", { name: "Open Sessions" })).toHaveAttribute("href", "#sessions");
  current = { ...current, resources: { allocations: 0, pending: 1 } };
  await page.getByRole("button", { name: "Check cleanup" }).click();
  await expect(page.getByRole("button", { name: "Change provider", exact: true })).toBeDisabled();
  current = { ...current, resources: { allocations: 0, pending: 0 } };
  await page.getByRole("button", { name: "Check cleanup" }).click();
  await page.getByRole("button", { name: "Change provider", exact: true }).click();
  await e2bForm(page);
  await expect(page.getByLabel("Core origin reachable from nodes and guests")).toHaveCount(0);
  await page.getByRole("button", { name: "Save provider and stay in maintenance" }).click();
  await expect(page.getByRole("button", { name: "Resume hosted placement" })).toBeEnabled();
  expect(writes).toHaveLength(2);
  expect(writes[1]?.body).toMatchObject({ core_url: "https://core.example", expected_generation: 1 });
  expect(current.maintenance).toBe(true);
  await page.getByRole("button", { name: "Resume hosted placement" }).click();
  expect(writes[2]).toEqual({ method: "PATCH", body: { maintenance: false, expected_generation: 2 } });
  await expect(page.getByRole("button", { name: "Enter maintenance to change provider" })).toBeVisible();
});

test("uncertain E2B writes clear secrets and require a successful refresh before another write", async ({ page }) => {
  let readsFail = false, attempts = 0;
  await page.route("**/core/v1/sandbox/deployment", (route) => {
    if (route.request().method() === "PUT") { attempts++; return route.fulfill({ status: 503, json: { error: { message: "fixture-private-e2b-key", code: "fixture-private-e2b-key" } } }); }
    return readsFail ? route.fulfill({ status: 503 }) : route.fulfill({ json: deployment({ maintenance: true, resources: { allocations: 0, pending: 0 } }) });
  });
  await openManager(page);
  await page.getByRole("button", { name: "Change provider", exact: true }).click();
  await e2bForm(page);
  await page.getByRole("button", { name: "Save provider and stay in maintenance" }).click();
  await expect(page.getByRole("alert")).toContainText("Refresh sandbox state to confirm");
  await expect(page.getByRole("alert")).not.toContainText("fixture-private-e2b-key");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  await expect(page.getByRole("button", { name: "Save provider and stay in maintenance" })).toBeDisabled();
  readsFail = true;
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("button", { name: "Change provider", exact: true })).toBeDisabled();
  readsFail = false;
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("button", { name: "Change provider", exact: true })).toBeEnabled();
  expect(attempts).toBe(1);
});

for (const theme of ["light", "dark"]) {
  test(`E2B configuration and maintenance fit desktop in ${theme}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ json: deployment({ provider: "e2b", mode: "direct", maintenance: true, e2b: { template, credential_configured: true } }) }));
    await openManager(page);
    await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
    await page.getByRole("button", { name: "Language and appearance" }).click();
    await page.getByRole("menuitemradio", { name: "简体中文" }).click();
    await expect(page.getByRole("heading", { name: "部署运行后端", exact: true })).toBeVisible();
    await expect(page.getByText("已配置", { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`e2b-provider-${theme}.png`), animations: "disabled" });
  });
}

test("failed activation stays in maintenance and requires refresh before an explicit resume retry", async ({ page }) => {
  const current = deployment({ provider: "e2b", mode: "direct", maintenance: true, e2b: { template, credential_configured: true }, resources: { allocations: 0, pending: 0 } });
  let attempts = 0;
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ json: current }));
  await page.route("**/core/v1/sandbox/deployment/maintenance", (route) => {
    attempts++;
    return route.fulfill({ status: 503, json: { error: { message: "Activation failed", code: "sandbox_provider_unavailable" } } });
  });
  await openManager(page);
  await page.getByRole("button", { name: "Resume hosted placement" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByRole("button", { name: "Resume hosted placement" })).toBeDisabled();
  await expect(page.getByText("Maintenance is enabled. New sandbox placement is paused.")).toBeVisible();
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("button", { name: "Resume hosted placement" })).toBeEnabled();
  expect(attempts).toBe(1);
});

test("changing location and leaving the configuration clear the transient E2B key", async ({ page }) => {
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ json: deployment({ maintenance: true, resources: { allocations: 0, pending: 0 } }) }));
  await openManager(page);
  await page.getByRole("button", { name: "Change provider", exact: true }).click();
  await e2bForm(page);
  await page.getByLabel("Immutable Runtime template", { exact: true }).fill("mutable-alias");
  await expect(page.getByText("Enter a template ID and build UUID separated by a colon.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save provider and stay in maintenance" })).toBeDisabled();
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Where to run sandboxes").selectOption("direct");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
  await e2bForm(page);
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Change provider", exact: true }).click();
  await page.getByLabel("Where to run sandboxes").selectOption("direct");
  await expect(page.getByLabel("E2B API key", { exact: true })).toHaveValue("");
});
