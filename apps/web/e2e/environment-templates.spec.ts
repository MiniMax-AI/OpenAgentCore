import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const collectionPath = "/v1/agents/environments/templates";
const beta = { "OpenAI-Beta": "agents=v1" };

interface Template {
  id: string;
  name: string | null;
  network: { access: "enabled" | "disabled"; allowed_domains: string[] };
  created_at: number;
  updated_at: number;
}

interface RecordedRequest {
  method: string;
  path: string;
  query: string;
  beta: string | null;
  body?: Record<string, unknown>;
}

async function control(request: APIRequestContext, values: Record<string, unknown>) {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/control`, { data: values })).ok()).toBe(true);
}

async function requests(request: APIRequestContext): Promise<RecordedRequest[]> {
  return (await request.get(`${fixtureBaseUrl}/__fixture/requests`)).json();
}

async function seed(request: APIRequestContext, name: string, access: "enabled" | "disabled" = "enabled"): Promise<Template> {
  const response = await request.post(`${fixtureBaseUrl}${collectionPath}`, { headers: beta, data: { name, network: { access } } });
  expect(response.status()).toBe(201);
  return response.json();
}

async function retrieve(request: APIRequestContext, id: string): Promise<Template> {
  const response = await request.get(`${fixtureBaseUrl}${collectionPath}/${id}`, { headers: beta });
  expect(response.ok()).toBe(true);
  return response.json();
}

async function openTemplates(page: Page) {
  await page.goto("/#templates");
  await expect(page.getByRole("heading", { name: "Environment Templates", exact: true })).toBeVisible();
}

function manager(page: Page) {
  return page.locator(".templates-page");
}

async function openSessionSelector(page: Page) {
  await page.getByRole("button", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: /^Start a Session with Second Agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Create a Session", exact: true });
  await dialog.getByRole("radio", { name: /Managed hosted/ }).check();
  return dialog.getByLabel("Reusable Environment Template");
}

test.beforeEach(async ({ request }) => {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
});

test.afterEach(async ({ request }) => {
  const entries = await requests(request);
  expect(entries.filter((entry) => entry.method !== "GET" && (
    entry.path.startsWith("/v1/agents/sessions") ||
    (entry.path.startsWith("/v1/agents/environments") && !entry.path.startsWith(collectionPath)) ||
    entry.path.startsWith("/api/v1/agent-daemon")
  ))).toEqual([]);
  expect(entries.filter((entry) => entry.path.startsWith(collectionPath)).every((entry) => entry.beta === "agents=v1")).toBe(true);
  const state = await (await request.get(`${fixtureBaseUrl}/__fixture/state`)).json();
  expect(state.sessions.map((session: { id: string }) => session.id)).toEqual(["session_snapshot"]);
});

test("opens Templates by navigation and hash, with an explicit empty catalog", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Templates", exact: true }).click();
  await expect(page).toHaveURL(/#templates$/u);
  await expect(manager(page).getByRole("heading", { name: "No Environment Templates yet" })).toBeVisible();
  await expect(manager(page).getByRole("button", { name: "New Template", exact: true })).toBeEnabled();
  await page.reload();
  await expect(page.getByRole("button", { name: "Templates", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(manager(page).getByRole("heading", { name: "Environment Templates", exact: true })).toBeVisible();
});

test("loads every Template page and keeps pagination cursors distinct", async ({ page, request }) => {
  const templates = await Promise.all(["First profile", "Second profile", "Third profile"].map((name) => seed(request, name)));
  await control(request, { environmentTemplatePageSize: 2 });
  await openTemplates(page);
  for (const template of templates) await expect(manager(page).getByRole("heading", { name: template.name!, exact: true })).toBeVisible();
  await expect(manager(page)).toContainText("3 of 3 Templates");
  const reads = (await requests(request)).filter((entry) => entry.method === "GET" && entry.path === collectionPath);
  expect(reads.some((entry) => new URLSearchParams(entry.query).has("after"))).toBe(true);
  expect(new Set(reads.map((entry) => entry.query)).size).toBeGreaterThanOrEqual(2);
  await manager(page).getByRole("searchbox", { name: "Filter Templates" }).fill("Third profile");
  await expect(manager(page)).toContainText("1 of 3 Templates");
  await expect(manager(page).getByRole("heading", { name: "First profile", exact: true })).toHaveCount(0);
});

for (const status of [400, 404, 405, 501, 503]) {
  test(`distinguishes unavailable Template catalog (${status}) from an empty catalog`, async ({ page, request }) => {
    await control(request, { environmentTemplateListStatus: status });
    await openTemplates(page);
    await expect(manager(page).getByText(
      status === 503 ? "Environment Templates could not be loaded" : "Environment Templates are not available",
      { exact: true },
    )).toBeVisible();
    await expect(manager(page).getByRole("heading", { name: "No Environment Templates yet" })).toHaveCount(0);
    await expect(manager(page).getByRole("button", { name: "New Template", exact: true })).toBeDisabled();
    await control(request, { environmentTemplateListStatus: 200 });
    await manager(page).getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(manager(page).getByRole("heading", { name: "No Environment Templates yet" })).toBeVisible();
  });
}

test("creates and edits basic Templates with minimal patches, then refreshes the Session selector", async ({ page, request }) => {
  await openTemplates(page);
  await manager(page).getByRole("button", { name: "New Template", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "Create Template", exact: true });
  await dialog.getByRole("textbox", { name: /^Name/ }).fill("Outbound disabled");
  await dialog.getByLabel("Network access", { exact: true }).selectOption("disabled");
  const beforeCreate = (await requests(request)).filter((entry) => entry.method === "GET" && entry.path === collectionPath).length;
  await dialog.getByRole("button", { name: "Create Template", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(manager(page).getByRole("heading", { name: "Outbound disabled", exact: true })).toBeVisible();
  const writes = (await requests(request)).filter((entry) => entry.method === "POST" && entry.path === collectionPath);
  expect(writes).toHaveLength(1);
  expect(writes[0]!.body).toEqual({ name: "Outbound disabled", network: { access: "disabled" } });
  const list = await (await request.get(`${fixtureBaseUrl}${collectionPath}`, { headers: beta })).json();
  const original = list.data[0] as Template;
  expect((await requests(request)).filter((entry) => entry.method === "GET" && entry.path === collectionPath).length).toBeGreaterThan(beforeCreate + 1);

  await manager(page).getByRole("button", { name: "Edit Outbound disabled", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Edit Template", exact: true });
  await dialog.getByRole("textbox", { name: /^Name/ }).fill("Reusable sandbox");
  await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  let updates = (await requests(request)).filter((entry) => entry.method === "POST" && entry.path === `${collectionPath}/${original.id}`);
  expect(updates.map((entry) => entry.body)).toEqual([{ name: "Reusable sandbox" }]);
  const renamed = await retrieve(request, original.id);
  expect({ ...renamed, name: original.name, updated_at: original.updated_at }).toEqual(original);

  await manager(page).getByRole("button", { name: "Edit Reusable sandbox", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Edit Template", exact: true });
  await dialog.getByLabel("Network access", { exact: true }).selectOption("enabled");
  await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  updates = (await requests(request)).filter((entry) => entry.method === "POST" && entry.path === `${collectionPath}/${original.id}`);
  expect(updates.at(-1)!.body).toEqual({ network: { access: "enabled" } });
  const changed = await retrieve(request, original.id);
  expect({ ...changed, network: renamed.network, updated_at: renamed.updated_at }).toEqual(renamed);

  const selector = await openSessionSelector(page);
  await expect(selector.locator(`option[value="${original.id}"]`)).toHaveText("Reusable sandbox · network enabled");
  await page.keyboard.press("Escape");
});

test("confirms deletion, preserves a failed delete, and removes the selected catalog entry after success", async ({ page, request }) => {
  const template = await seed(request, "Delete candidate");
  await openTemplates(page);
  await manager(page).getByRole("button", { name: "Delete Delete candidate", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "Delete Template?", exact: true });
  await expect(dialog).toContainText("Existing Sessions keep their configuration");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect((await requests(request)).filter((entry) => entry.method === "DELETE")).toEqual([]);

  await control(request, { environmentTemplateDeleteStatus: 503 });
  await manager(page).getByRole("button", { name: "Delete Delete candidate", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "Delete Template?", exact: true });
  await dialog.getByRole("button", { name: "Delete Template", exact: true }).click();
  await expect(manager(page).getByRole("alert")).toContainText("not confirmed");
  expect((await retrieve(request, template.id)).name).toBe("Delete candidate");
  expect((await requests(request)).filter((entry) => entry.method === "DELETE")).toHaveLength(1);
  await manager(page).getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(manager(page).getByRole("button", { name: "Delete Delete candidate", exact: true })).toBeEnabled();
  await manager(page).getByRole("button", { name: "Delete Delete candidate", exact: true }).click();
  await page.getByRole("dialog", { name: "Delete Template?", exact: true }).getByRole("button", { name: "Delete Template", exact: true }).click();
  await expect(manager(page).getByRole("heading", { name: "No Environment Templates yet" })).toBeVisible();
  const selector = await openSessionSelector(page);
  await expect(selector.locator(`option[value="${template.id}"]`)).toHaveCount(0);
  await expect(selector).toBeDisabled();
  await page.keyboard.press("Escape");
});

test("rejects advanced Template responses without offering destructive basic edits", async ({ page, request }) => {
  await seed(request, "Advanced profile");
  await page.route(`**${collectionPath}?*`, async (route) => {
    const response = await route.fetch();
    const value = await response.json();
    value.data[0].packages.npm = ["private-package"];
    await route.fulfill({ response, json: value });
  });
  await openTemplates(page);
  await expect(manager(page).getByText("Environment Templates could not be loaded", { exact: true })).toBeVisible();
  await expect(manager(page).getByRole("button", { name: "Edit Advanced profile", exact: true })).toHaveCount(0);
  await expect(manager(page).getByRole("button", { name: "New Template", exact: true })).toBeDisabled();
});

for (const operation of ["create", "update"] as const) {
  test(`does not replay a failed Template ${operation} or silently change the catalog`, async ({ page, request }) => {
    const template = operation === "update" ? await seed(request, "Original name", "disabled") : null;
    await control(request, { [operation === "create" ? "environmentTemplateCreateStatus" : "environmentTemplateUpdateStatus"]: 503 });
    await openTemplates(page);
    await manager(page).getByRole("button", { name: template ? "Edit Original name" : "New Template", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: template ? "Edit Template" : "Create Template", exact: true });
    await dialog.getByRole("textbox", { name: /^Name/ }).fill("Rejected name");
    await dialog.getByRole("button", { name: template ? "Save changes" : "Create Template", exact: true }).click();
    await expect(manager(page).getByRole("alert")).toContainText("not confirmed");
    await expect(manager(page).getByRole("button", { name: "New Template", exact: true })).toBeDisabled();
    const path = template ? `${collectionPath}/${template.id}` : collectionPath;
    expect((await requests(request)).filter((entry) => entry.method === "POST" && entry.path === path)).toHaveLength(1);
    if (template) expect(await retrieve(request, template.id)).toEqual(template);
    await manager(page).getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(manager(page).getByRole("button", { name: "New Template", exact: true })).toBeEnabled();
    await expect(manager(page).getByRole("heading", { name: "Rejected name", exact: true })).toHaveCount(0);
  });
}

for (const operation of ["read", "write"] as const) {
  test(`fences a late Template ${operation} after switching Core connections`, async ({ page, request }) => {
    const original = await seed(request, "Old connection Template");
    await page.addInitScript(({ original, operation, collectionPath }) => {
      const target = window as Window & {
        holdTemplateResponse?: boolean;
        releaseTemplateResponse?: () => void;
        newTemplateReads?: number;
      };
      const originalFetch = window.fetch.bind(window);
      target.holdTemplateResponse = false;
      target.newTemplateReads = 0;
      const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
      const list = (template: typeof original) => ({ object: "list", data: [template], has_more: false, first_id: template.id, last_id: template.id });
      window.fetch = (input, init) => {
        const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
        const method = init?.method ?? "GET";
        if (new Headers(init?.headers).get("Authorization") === "Bearer replacement-token" && url.pathname === collectionPath) {
          target.newTemplateReads = (target.newTemplateReads ?? 0) + 1;
          return Promise.resolve(json(list({ ...original, id: "49999999-1111-4111-8111-111111111111", name: "New connection Template" })));
        }
        if (target.holdTemplateResponse && (
          operation === "read" ? url.pathname === collectionPath && method === "GET" : url.pathname === `${collectionPath}/${original.id}` && method === "POST"
        )) {
          target.holdTemplateResponse = false;
          // Deliberately ignore abort: fencing must also handle a response that
          // was already accepted by the remote service before cancellation.
          return new Promise<Response>((resolve) => {
            target.releaseTemplateResponse = () => resolve(json(operation === "read" ? list(original) : { ...original, name: "Late old name" }));
          });
        }
        return originalFetch(input, init);
      };
    }, { original, operation, collectionPath });
    await openTemplates(page);
    await expect(manager(page).getByRole("heading", { name: original.name!, exact: true })).toBeVisible();
    await page.evaluate(() => { (window as Window & { holdTemplateResponse?: boolean }).holdTemplateResponse = true; });
    if (operation === "read") {
      await manager(page).getByRole("button", { name: "Refresh", exact: true }).click();
    } else {
      await manager(page).getByRole("button", { name: `Edit ${original.name}`, exact: true }).click();
      const dialog = page.getByRole("dialog", { name: "Edit Template", exact: true });
      await dialog.getByRole("textbox", { name: /^Name/ }).fill("Late old name");
      await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
    }
    await expect.poll(() => page.evaluate(() => typeof (window as Window & { releaseTemplateResponse?: () => void }).releaseTemplateResponse)).toBe("function");
    // Changing the hash is the same navigation available in the address bar;
    // it lets an in-flight write finish after its owning page has unmounted.
    await page.evaluate(() => { location.hash = "system"; });
    await expect(page.getByRole("dialog", { name: "Edit Template", exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: "Configure Agent Core connection", exact: true }).click();
    const connection = page.getByRole("dialog", { name: "Connect an Agent Core", exact: true });
    await connection.getByRole("radio", { name: /Other compatible Core/ }).check();
    await connection.getByLabel("Compatible Core base URL").fill(`${new URL(page.url()).origin}/v1`);
    await connection.getByLabel("Bearer token").fill("replacement-token");
    await connection.getByRole("button", { name: "Apply connection", exact: true }).click();
    await page.getByRole("button", { name: "Templates", exact: true }).click();
    await expect(manager(page).getByRole("heading", { name: "New connection Template", exact: true })).toBeVisible();
    const before = await page.evaluate(() => (window as Window & { newTemplateReads?: number }).newTemplateReads);
    await page.evaluate(async () => {
      (window as Window & { releaseTemplateResponse?: () => void }).releaseTemplateResponse?.();
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    });
    await expect(manager(page).getByRole("heading", { name: "New connection Template", exact: true })).toBeVisible();
    await expect(manager(page)).not.toContainText("Late old name");
    await expect(manager(page)).not.toContainText("Old connection Template");
    expect(await page.evaluate(() => (window as Window & { newTemplateReads?: number }).newTemplateReads)).toBe(before);
  });
}

test("keeps the Template modal keyboard-contained and usable at narrow widths", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openTemplates(page);
  const trigger = manager(page).getByRole("button", { name: "New Template", exact: true });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Create Template", exact: true });
  await expect(dialog.getByRole("textbox", { name: /^Name/ })).toBeFocused();
  await dialog.getByRole("button", { name: "Close dialog", exact: true }).focus();
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.getByRole("button", { name: "Create Template", exact: true })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "Close dialog", exact: true })).toBeFocused();
  const sizes = await dialog.evaluate((element) => ({
    viewport: innerWidth,
    document: document.documentElement.scrollWidth,
    left: element.getBoundingClientRect().left,
    right: element.getBoundingClientRect().right,
  }));
  expect(sizes.document).toBeLessThanOrEqual(sizes.viewport);
  expect(sizes.left).toBeGreaterThanOrEqual(0);
  expect(sizes.right).toBeLessThanOrEqual(sizes.viewport);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});
