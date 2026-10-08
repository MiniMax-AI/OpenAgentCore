import { expect, test, type Page } from "@playwright/test";

import { expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

const historySnapshot = (page: Page) => page.evaluate(() => ({
  url: window.location.href,
  length: window.history.length,
  state: window.history.state,
}));

async function tabBackToSkipLink(page: Page) {
  const skip = page.getByRole("link", { name: "Skip to main content", exact: true });
  // Reach the link through the actual tab order, including after a route change
  // or the tour restores focus to its opener.
  for (let step = 0; step < 30; step++) {
    if (await skip.evaluate((element) => element === document.activeElement)) break;
    await page.keyboard.press("Shift+Tab");
  }
  await expect(skip).toBeFocused();
  await expect(skip).toBeInViewport();
  return skip;
}

test("skips to the current page with Enter without changing its route or history", async ({ page, request }) => {
  await openConsole(page, request, "files");
  const heading = page.getByRole("heading", { name: "Files", level: 1 });
  await expect(heading).toBeVisible();
  const skip = page.getByRole("link", { name: "Skip to main content", exact: true });
  const main = page.getByRole("main");
  const existingMain = await main.elementHandle();
  const history = await historySnapshot(page);

  await expect(skip).not.toBeInViewport();
  await page.keyboard.press("Tab");
  await expect(skip).toBeFocused();
  await expect(skip).toBeInViewport();
  await page.keyboard.press("Enter");

  await expect(page).toHaveURL(history.url);
  await expect(heading).toBeVisible();
  await expect(main).toBeFocused();
  expect(await existingMain!.evaluate((element) => element === document.activeElement)).toBe(true);
  expect(await historySnapshot(page)).toEqual(history);

  await page.keyboard.press("Tab");
  expect(await main.evaluate((element) => element !== document.activeElement && element.contains(document.activeElement))).toBe(true);
});

test("preserves detail parameters and Back/Forward history after repeated skip-link activation", async ({ page, request }) => {
  await openConsole(page, request, "agents?project=proj_7f3a91c2");
  await page.getByRole("button", { name: "Open Spec drafter", exact: true }).click();
  const heading = page.getByRole("heading", { name: "Spec drafter", level: 1 });
  await expect(heading).toBeVisible();
  const history = await historySnapshot(page);
  const params = new URLSearchParams(new URL(history.url).hash.split("?")[1]);
  expect(params.get("project")).toBe("proj_7f3a91c2");
  expect(params.get("id")).toBeTruthy();
  const main = page.getByRole("main");
  const existingMain = await main.elementHandle();
  await expect(main).toBeFocused();

  for (const activation of ["click", "Enter"]) {
    const skip = await tabBackToSkipLink(page);
    if (activation === "click") await skip.click();
    else await page.keyboard.press("Enter");
    await expect(page).toHaveURL(history.url);
    await expect(heading).toBeVisible();
    await expect(main).toBeFocused();
    expect(await existingMain!.evaluate((element) => element === document.activeElement)).toBe(true);
    expect(await historySnapshot(page)).toEqual(history);
  }

  await page.goBack();
  await expect(page).toHaveURL(/#agents\?project=proj_7f3a91c2$/);
  await expect(page.getByRole("heading", { name: "Agents", level: 1 })).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(history.url);
  await expect(heading).toBeVisible();
  await page.getByRole("button", { name: "Back", exact: true }).first().click();
  await expect(page).toHaveURL(/#agents\?project=proj_7f3a91c2$/);
});

test("skips to the current main after the tour removes and remounts the console", async ({ page, request }) => {
  await openConsole(page, request);
  await page.getByRole("button", { name: "Show Getting started", exact: true }).click();
  const opener = page.getByRole("button", { name: "Take the tour", exact: true });
  const originalMain = await page.locator("main.app-main#main-content").elementHandle();
  await opener.click();
  await expect(page.getByRole("heading", { name: "Is it healthy, and where does it fail?", exact: true })).toBeVisible();
  await expect.poll(() => originalMain!.evaluate((element) => element.isConnected)).toBe(false);
  await expect(page.getByRole("link", { name: "Skip to main content", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Skip", exact: true }).click();
  await expect(opener).toBeFocused();

  const history = await historySnapshot(page);
  await tabBackToSkipLink(page);
  await page.keyboard.press("Enter");
  await expect(page.locator("main.app-main#main-content")).toBeFocused();
  expect(await originalMain!.evaluate((element) => element.isConnected)).toBe(false);
  await expect(page).toHaveURL(history.url);
  expect(await historySnapshot(page)).toEqual(history);
});
