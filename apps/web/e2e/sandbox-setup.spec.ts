import { expect, test, type Page } from "@playwright/test";

const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const setupUrl = `${fixture}/core/v1/sandbox/deployment`;
async function openSetup(page: Page) {
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Set up hosted sandboxes" })).toBeVisible();
}
test.beforeEach(async ({ page, request }) => {
  await page.route("**/console/config", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) }) }));
  await request.post(`${fixture}/__fixture/reset`);
  await request.post(`${fixture}/__fixture/sandbox-uninitialized`);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sessions", exact: true })).toBeVisible();
});

test("bundled console needs no extra admin key and provides one install command with automatic node status", async ({ page, request }) => {
  await page.route("**/console/config", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) }) }));
  const browserAuthorizations: Array<string | undefined> = [];
  await page.route("**/core/v1/sandbox/**", async (route) => {
    browserAuthorizations.push(route.request().headers().authorization);
    await route.continue();
  });
  await page.reload();
  await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Set up hosted sandboxes" })).toBeVisible();
  await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
  await expect(page.getByLabel("Core origin reachable from nodes and guests")).toBeVisible();
  await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await page.getByRole("button", { name: "Add node", exact: true }).click();
  const command = page.getByLabel("One-time enrollment command");
  await expect(command).toHaveValue(/fixture-once-token/);
  await expect(command).toHaveValue(/\/node-install\/node-install.pyz/);

  await expect(page.getByRole("button", { name: "Copy node command" })).toBeVisible();
  await expect(page.getByRole("dialog").getByRole("status")).toContainText("Waiting for your node");
  await expect(page.locator(".sandbox-manager")).not.toContainText("Create a private /etc/parsar/sandbox-node.json");
  await request.post(`${fixture}/__fixture/sandbox-add-node`);
  await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Enrolled host", { timeout: 10000 });
  await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Available");
  expect(browserAuthorizations.length).toBeGreaterThan(0);
  expect(browserAuthorizations.every((value) => value === undefined)).toBe(true);
});

for (const provider of ["docker", "microsandbox"]) {
  test(`initial ${provider} setup, enrollment and refresh`, async ({ page, request }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openSetup(page);
    const submit = page.getByRole("button", { name: "Initialize sandbox deployment" });
    await expect(submit).toBeDisabled();
    await expect(page.getByLabel("Where to run sandboxes")).toHaveValue("");
    await expect(page.getByRole("button", { name: "Add node", exact: true })).toHaveCount(0);
    await page.getByLabel("Where to run sandboxes").selectOption("nodes");
    await page.getByLabel("Sandbox provider").selectOption(provider);
    const origin = page.getByLabel("Core origin reachable from nodes and guests");
    for (const invalid of ["http://127.0.0.1:8080", "https://localhost", "https://[::1]", "http://core.example", "https://core.example/v1", "https://user:secret@core.example", "https://core.example?key=secret"]) {
      await origin.fill(invalid);
      await expect(submit).toBeDisabled();
    }
    await origin.fill("https://CORE.example/");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await submit.click();
    await page.getByText("Deployment details", { exact: true }).click();
    await expect(page.locator(".sandbox-deployment-details")).toContainText(provider === "docker" ? "Docker" : "microsandbox");
    await expect(page.getByLabel("Sandbox provider")).toHaveCount(0);
    await expect(page.getByText("No nodes registered. Add a node to provide hosted capacity.")).toBeVisible();
    await page.getByRole("button", { name: "Add node", exact: true }).click();
    await expect(page.getByLabel("One-time enrollment command")).toHaveValue(/--core-url 'https:\/\/core.example'/);
    expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toMatch(/fixture-admin-key|fixture-once-token/);
    await request.post(`${fixture}/__fixture/sandbox-add-node`);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Refresh sandbox state" }).click();
    await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Enrolled host");
    await expect(page.getByRole("region", { name: "Sandbox nodes", exact: true })).toContainText("Available");
    const state = await (await request.get(`${fixture}/__fixture/sandbox`)).json();
    expect(state.calls.filter((call: { path: string; method: string }) => call.path.endsWith("/deployment") && call.method === "POST")).toHaveLength(1);
    expect(state.provider).toBe(provider);
    expect(state.core_url).toBe("https://core.example");
  });
}

test("concurrent setup conflict requires refresh and displays the committed provider", async ({ page, request }) => {
  await openSetup(page);
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
  const winner = { provider: "microsandbox", core_url: "https://other-core.example" };
  expect((await request.post(setupUrl, { data: winner })).status()).toBe(200);
  expect((await request.post(setupUrl, { data: winner })).status()).toBe(200);
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await expect(page.getByRole("alert")).toContainText("already configured");
  await expect(page.getByRole("button", { name: "Initialize sandbox deployment" })).toBeDisabled();
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await page.getByText("Deployment details", { exact: true }).click();
  await expect(page.locator(".sandbox-deployment-details")).toContainText("microsandbox");
  await expect(page.locator(".sandbox-deployment-details")).toContainText(winner.core_url);
  await expect(page.getByLabel("Sandbox provider")).toHaveCount(0);
});

test("a lost setup response is not retried and refresh recovers the saved deployment", async ({ page, request }) => {
  await openSetup(page);
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
  let writes = 0;
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    writes++;
    await route.fetch();
    await route.abort("connectionreset");
  });
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await expect(page.getByRole("alert")).toContainText("Refresh sandbox state to confirm");
  await expect(page.getByRole("button", { name: "Initialize sandbox deployment" })).toBeDisabled();
  expect(writes).toBe(1);
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await page.getByText("Deployment details", { exact: true }).click();
  await expect(page.locator(".sandbox-deployment-details")).toContainText("Docker");
  expect(writes).toBe(1);
  expect((await (await request.get(setupUrl)).json()).provider).toBe("docker");
});

test("a failed setup refresh keeps setup disabled until a successful read", async ({ page }) => {
  await openSetup(page);
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
  await page.route("**/core/v1/sandbox/deployment", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "Deployment unavailable" } }) }));
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await expect(page.getByRole("alert")).toContainText("Refresh sandbox state to confirm");
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("alert")).toContainText("The sandbox service is unavailable");
  await expect(page.getByLabel("Where to run sandboxes")).toBeDisabled();
  await page.unroute("**/core/v1/sandbox/deployment");
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByLabel("Where to run sandboxes")).toBeEnabled();
  await expect(page.getByLabel("Where to run sandboxes")).toHaveValue("");
});

for (const operation of ["setup", "enrollment"] as const) {
  test(`Core connection changes discard forms and a late ${operation} result`, async ({ page, request }) => {
    await page.addInitScript((operation) => {
      const target = window as Window & { releaseSandboxResponse?: () => void };
      const originalFetch = window.fetch.bind(window);
      window.fetch = (input, init) => {
        const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
        if (init?.method === "POST" && url.pathname === `/core/v1/sandbox/${operation === "setup" ? "deployment" : "enrollment-tokens"}`) {
          // Ignore cancellation to check a result accepted before unmounting.
          return new Promise<Response>((resolve) => {
            target.releaseSandboxResponse = () => resolve(new Response(JSON.stringify(operation === "setup" ? { installation_id: "old-installation", provider: "docker", core_url: "https://old.example", maintenance: false, owner_epoch: 1 } : { token: "old-secret-token", expires_at: "2026-09-23T09:00:00Z" }), { status: 200 }));
          });
        }
        return originalFetch(input, init);
      };
    }, operation);
    await page.reload();
    await openSetup(page);
    if (operation === "setup") {
      await page.getByLabel("Where to run sandboxes").selectOption("nodes");
      await page.getByLabel("Sandbox provider").selectOption("docker");
      await page.getByLabel("Core origin reachable from nodes and guests").fill("https://core.example");
      await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
    } else {
      await request.post(setupUrl, { data: { provider: "docker", core_url: "https://core.example" } });
      await page.getByRole("button", { name: "Refresh sandbox state" }).click();
      await page.getByRole("button", { name: "Add node", exact: true }).click();
    }
    await expect.poll(() => page.evaluate(() => typeof (window as Window & { releaseSandboxResponse?: () => void }).releaseSandboxResponse)).toBe("function");
    await page.evaluate(() => { location.hash = "system"; });
    await page.getByRole("button", { name: "Configure Agent Core connection", exact: true }).click();
    const connection = page.getByRole("dialog", { name: "Connect an Agent Core", exact: true });
    await connection.getByRole("radio", { name: /Other compatible Core/ }).check();
    await connection.getByLabel("Compatible Core base URL").fill(`${new URL(page.url()).origin}/v1`);
    await connection.getByLabel("Bearer token").fill("replacement-token");
    await connection.getByRole("button", { name: "Apply connection", exact: true }).click();
    await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
    await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
    await expect(page.locator(".sandbox-manager")).toContainText("Switch the Core connection to /v1");
    await page.evaluate(() => { (window as Window & { releaseSandboxResponse?: () => void }).releaseSandboxResponse?.(); });
    await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
    await expect(page.locator(".sandbox-manager")).toContainText("Switch the Core connection to /v1");
    await expect(page.getByLabel("One-time enrollment command")).toHaveCount(0);
    await expect(page.locator(".sandbox-manager")).not.toContainText("old-installation");
    expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toMatch(/fixture-admin-key|old-secret-token/);
  });
}

test("unpaired or unavailable consoles show setup guidance without admin credentials or manager requests", async ({ page, request }) => {
  for (const body of [{ sandbox_admin: false, node_installer: true }, null]) {
    await page.route("**/console/config", (route) => route.fulfill(body ? { contentType: "application/json", body: JSON.stringify(body) } : { status: 404, body: "Not found" }));
    await page.reload();
    await page.getByRole("button", { name: "Hosted Sandbox Manager", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("Sandbox administration is not configured");
    await expect(page.getByLabel("Deployment admin key")).toHaveCount(0);
    expect((await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls).toHaveLength(0);
  }
  await page.route("**/console/config", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) }) }));
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("heading", { name: "Set up hosted sandboxes" })).toBeVisible();
});

test("loopback console setup exposes the address and cannot save the automatic default", async ({ page, request }) => {
  await openSetup(page);
  const origin = page.getByLabel("Core origin reachable from nodes and guests");
  await expect(origin).toBeVisible();
  await expect(origin).toHaveValue(new URL(page.url()).origin);
  await expect(page.getByText("This console address cannot be used by sandbox guests.", { exact: false })).toBeVisible();
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  const submit = page.getByRole("button", { name: "Initialize sandbox deployment" });
  await expect(submit).toBeDisabled();
  await origin.press("Enter");
  expect((await (await request.get(`${fixture}/__fixture/sandbox`)).json()).calls.filter((call: { method: string }) => call.method === "POST")).toHaveLength(0);
  await origin.fill("https://core.example");
  await submit.click();
  expect((await (await request.get(setupUrl)).json()).core_url).toBe("https://core.example");
});

test("a usable HTTPS console origin initializes without exposing the network field", async ({ page, request }) => {
  const localOrigin = new URL(page.url()).origin;
  await page.route("https://core.example/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/console/config") return route.fallback();
    if (url.pathname.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: "" });
    const response = await route.fetch({ url: `${localOrigin}${url.pathname}${url.search}` });
    await route.fulfill({ response });
  });
  await page.goto("https://core.example/");
  await openSetup(page);
  await expect(page.getByLabel("Core origin reachable from nodes and guests")).toBeHidden();
  await page.getByLabel("Where to run sandboxes").selectOption("nodes");
  await page.getByLabel("Sandbox provider").selectOption("docker");
  await page.getByRole("button", { name: "Initialize sandbox deployment" }).click();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeVisible();
  expect((await (await request.get(setupUrl)).json()).core_url).toBe("https://core.example");
});
