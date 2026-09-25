import { expect, test } from "@playwright/test";

import { expectManagementBoundary, observeBrowser, resetFixture } from "./console";

test.afterEach(async ({ request, page }) => expectManagementBoundary(request, page));

test("creates the administrator, keeps no credential in the browser, and signs out and back in", async ({ page, request }) => {
  await resetFixture(request);
  observeBrowser(page);
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
