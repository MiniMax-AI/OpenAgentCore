import { expect, test, type Page } from "@playwright/test";

import { expectManagementBoundary, FIXTURE_CORE_KEY, openConsole, resetFixture } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

async function signIn(page: Page, coreKey: string) {
  await page.getByLabel("Core key", { exact: true }).fill(coreKey);
  await page.getByRole("button", { name: "Sign in" }).click();
}

const browserStorage = (page: Page) => page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }));

test("signs in with the Core key, keeps it out of the browser, and signs out and back in", async ({ page, request }) => {
  await resetFixture(request, "login");
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Sign in to Parsar Core" })).toBeVisible();
  await signIn(page, "not-the-core-key");
  await expect(page.getByRole("alert")).toHaveText("This Core key is not correct. Check it and try again.");
  await signIn(page, FIXTURE_CORE_KEY);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  expect(await browserStorage(page)).not.toContain(FIXTURE_CORE_KEY);

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to Parsar Core" })).toBeVisible();
  await signIn(page, FIXTURE_CORE_KEY);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();

  // Repeated wrong keys: the console says how long to wait, from Retry-After.
  await page.getByRole("button", { name: "Sign out" }).click();
  for (let attempt = 0; attempt < 3; attempt++) await signIn(page, "not-the-core-key");
  await expect(page.getByRole("alert")).toHaveText("Too many attempts. Try again in 30 seconds.");
  // Failed attempts never lock out the right key.
  await signIn(page, FIXTURE_CORE_KEY);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  expect(await browserStorage(page)).not.toContain(FIXTURE_CORE_KEY);
});

test("opens a fresh install on the Overview's Getting started: a project and its key shown once, then the step is done", async ({ page, request }) => {
  await resetFixture(request, "login", { fresh: true });
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");
  await signIn(page, FIXTURE_CORE_KEY);

  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  const step = (name: string) => page.getByRole("region", { name: "Getting started" }).getByRole("listitem").filter({ hasText: name });
  // The fixture deployment already has a ready node.
  await expect(step("Get sandboxes ready")).toContainText("Done");
  await expect(step("Set a default model")).toContainText("To do");
  await expect(step("Create a project and issue a key")).toContainText("To do");
  await expect(step("Run the first Session")).toContainText("To do");

  // The tour stays optional.
  await page.getByRole("button", { name: "Take the tour" }).click();
  await expect(page.getByRole("heading", { name: "Is it healthy, and where does it fail?" })).toBeVisible();
  await page.getByRole("button", { name: "Skip" }).click();

  await step("Create a project and issue a key").getByRole("button", { name: "Create project" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("My app");
  // Core's project list answers late: the new project's key dialog must not wait for it.
  const projectList = /\/core\/v1\/projects(\?.*)?$/;
  await page.route(projectList, async (route) => {
    if (route.request().method() === "GET") await new Promise((resolve) => setTimeout(resolve, 5_000));
    await route.fallback();
  });
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  const issue = page.getByRole("dialog", { name: "Issue a key for My app" });
  await expect(issue).toBeVisible({ timeout: 3_000 });
  await page.unroute(projectList);
  await issue.getByLabel("Key name").fill("my-app");
  await issue.getByRole("button", { name: "Issue key" }).click();
  const issued = page.getByRole("dialog", { name: "Key issued" });
  await expect(issued.getByLabel("New key my-app")).toHaveValue(/fixture-secret/);
  await issued.getByRole("button", { name: "I've saved this key" }).click();
  await expect(page.getByLabel("New key my-app")).toHaveCount(0);

  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await expect(step("Create a project and issue a key")).toContainText("Done");
  const stored = await browserStorage(page);
  expect(stored).not.toContain("fixture-secret");
  expect(stored).not.toContain(FIXTURE_CORE_KEY);
});

test("leads from Getting started to the default model, and counts it done once the default harness has one", async ({ page, request }) => {
  await openConsole(page, request, "overview", { fresh: true });
  const step = page.getByRole("region", { name: "Getting started" }).getByRole("listitem").filter({ hasText: "Set a default model" });
  await expect(step).toContainText("To do");
  await step.getByRole("button", { name: "Open System" }).click();
  await expect(page.getByRole("heading", { name: "System", level: 1 })).toBeVisible();
  // It arrives on the default harness's action.
  const set = page.getByRole("button", { name: "Set the default model for Codex" });
  await expect(set).toBeFocused();
  await set.click();
  const form = page.getByRole("dialog", { name: "Set default model for Codex" });
  await form.getByLabel("Base URL").fill("https://model.example/v1");
  await form.getByLabel("API key").fill("sk-fixture-getting-started");
  await form.getByRole("button", { name: "Save" }).click();
  await expect(form).toBeHidden();
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await expect(step).toContainText("Done");
});

test("shows every page empty on a fresh install, and Getting started hides, comes back and leads to sandbox setup", async ({ page, request }) => {
  await openConsole(page, request, "overview", { fresh: true, sandbox: "none", nodes: "none" });
  // What each page shows once its reads are done: an empty state, Core's own figures, or sandbox setup.
  const loaded = (view: string) => view === "core-metrics" ? page.locator(".kpi-strip").first()
    : view === "nodes" ? page.getByRole("heading", { name: "Where should sandboxes run?" })
    : page.locator(".console-empty").first();
  for (const view of ["core-metrics", "agent-metrics", "sandbox-metrics", "sessions", "agents", "templates", "skills", "files", "vaults", "projects", "nodes", "system", "overview"]) {
    await page.goto(`/#${view}`);
    await expect(loaded(view)).toBeVisible();
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
    await expect(page.getByText(/Loading|Connecting/)).toHaveCount(0);
    // No failed read: neither an error on the page nor an error toast.
    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(page.locator(".toast-region-assertive")).toBeEmpty();
  }

  const checklist = page.getByRole("region", { name: "Getting started" });
  await checklist.getByRole("button", { name: "Hide Getting started" }).click();
  await expect(checklist).toHaveCount(0);
  await page.reload();
  await expect(page.locator(".console-empty").first()).toBeVisible();
  await expect(checklist).toHaveCount(0);
  await page.getByRole("button", { name: "Show Getting started" }).click();
  const step = checklist.getByRole("listitem").filter({ hasText: "Get sandboxes ready" });
  await expect(step).toContainText("To do");
  await step.getByRole("button", { name: "Set up sandboxes" }).click();
  await expect(page.getByRole("heading", { name: "Where should sandboxes run?" })).toBeVisible();
});

test("opens Add node from Getting started when no node has joined", async ({ page, request }) => {
  await openConsole(page, request, "overview", { fresh: true, nodes: "none" });
  const step = page.getByRole("region", { name: "Getting started" }).getByRole("listitem").filter({ hasText: "Get sandboxes ready" });
  await expect(step).toContainText("To do");
  await step.getByRole("button", { name: "Add node" }).click();
  await expect(page.getByRole("heading", { name: "Nodes", level: 1 })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Add node" })).toBeVisible();
});

test("reopens a finished Getting started from the sidebar and keeps You're set through the tour", async ({ page, request }) => {
  await openConsole(page, request);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Show Getting started" }).click();
  const done = page.getByRole("region", { name: "You're set" });
  await expect(done).toBeVisible();
  await done.getByRole("button", { name: "Take the tour" }).click();
  await page.getByRole("button", { name: "Skip" }).click();
  await expect(done.getByRole("button", { name: "Take the tour" })).toBeFocused();
  await done.getByRole("button", { name: "Dismiss" }).click();
  await expect(done).toHaveCount(0);
});
