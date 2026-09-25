import { expect, test } from "@playwright/test";

import { expectManagementBoundary, resetFixture } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("creates the administrator, keeps no credential in the browser, and signs out and back in", async ({ page, request }) => {
  await resetFixture(request, "setup");
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Create your administrator account" })).toBeVisible();
  await page.locator("input[name=username]").fill("admin");
  await page.locator("input[name=password]").fill("correct horse battery");
  await page.locator("input[name=confirm]").fill("correct horse battery");
  await page.getByRole("button", { name: "Create administrator account" }).click();
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();

  const stored = await page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }));
  expect(stored).not.toContain("correct horse battery");

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await page.locator("input[name=username]").fill("admin");
  await page.locator("input[name=password]").fill("wrong password!!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await page.locator("input[name=password]").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
});

test("sets up a fresh install: first project, a key shown once, the tour, then the console", async ({ page, request }) => {
  await resetFixture(request, "setup", { fresh: true });
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.language", "en"));
  await page.goto("/");
  await page.locator("input[name=username]").fill("admin");
  await page.locator("input[name=password]").fill("correct horse battery");
  await page.locator("input[name=confirm]").fill("correct horse battery");
  await page.getByRole("button", { name: "Create administrator account" }).click();

  await expect(page.getByRole("heading", { name: "Create your first project" })).toBeVisible();
  await page.locator("input[name=key-name]").fill("my-app");
  await page.getByRole("button", { name: "Create project and key" }).click();
  await expect(page.getByLabel("New key my-app")).toHaveValue(/fixture-secret/);
  await page.getByRole("button", { name: "I've saved it, continue" }).click();

  await expect(page.getByLabel("New key my-app")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Is it healthy, and where does it fail?" })).toBeVisible();
  await page.getByRole("button", { name: "Skip" }).click();
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  expect(await page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }))).not.toContain("fixture-secret");
});
