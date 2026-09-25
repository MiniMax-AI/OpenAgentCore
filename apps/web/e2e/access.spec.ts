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

test("opens a fresh install on the Overview without asking for a project first", async ({ page, request }) => {
  await resetFixture(request, "login", { fresh: true });
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");
  await signIn(page, FIXTURE_CORE_KEY);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  expect(await browserStorage(page)).not.toContain(FIXTURE_CORE_KEY);
});
