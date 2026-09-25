import { expect, test, type Page } from "@playwright/test";

import { expectManagementBoundary, FIXTURE_CORE_KEY, resetFixture } from "./console";

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

test("sets up a fresh install: Core key sign-in, first project, a key shown once, the tour, then the console", async ({ page, request }) => {
  await resetFixture(request, "login", { fresh: true });
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");
  await signIn(page, FIXTURE_CORE_KEY);

  await expect(page.getByRole("heading", { name: "Create your first project" })).toBeVisible();
  await page.locator("input[name=key-name]").fill("my-app");
  await page.getByRole("button", { name: "Create project and key" }).click();
  await expect(page.getByLabel("New key my-app")).toHaveValue(/fixture-secret/);
  await page.getByRole("button", { name: "I've saved it, continue" }).click();

  await expect(page.getByLabel("New key my-app")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Is it healthy, and where does it fail?" })).toBeVisible();
  await page.getByRole("button", { name: "Skip" }).click();
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  const stored = await browserStorage(page);
  expect(stored).not.toContain("fixture-secret");
  expect(stored).not.toContain(FIXTURE_CORE_KEY);
});
