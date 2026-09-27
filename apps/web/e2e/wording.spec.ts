import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

for (const language of ["en", "zh-CN"] as const) {
  test(`explains that applications submit function results across the console (${language})`, async ({ page, request }) => {
    await openConsole(page, request, "overview");
    if (language !== "en") {
      await page.getByRole("button", { name: "Language and appearance" }).click();
      await page.getByRole("menuitemradio", { name: "简体中文" }).click();
    }
    const reason = language === "en" ? "Waiting for the application to submit the result of approve_refund." : "等待应用提交 approve_refund 的结果。";
    const responsibility = language === "en" ? "The calling application must submit this result; the console cannot submit it." : "此结果须由调用方应用提交，控制台无法代为提交。";
    await expect(page.getByText(reason, { exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: language === "en" ? "Session log" : "Session 日志", exact: true }).first().click();
    await page.getByRole("radio", { name: language === "en" ? /^Waiting for caller/ : /^等待调用方/ }).click();
    const row = page.getByRole("row").filter({ hasText: reason }).first();
    await expect(row.getByText(reason, { exact: true })).toBeVisible();
    await row.locator(".name-cell-link").click();
    await expect(page.getByText(reason, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(responsibility, { exact: true })).toBeVisible();
  });
}

test("keeps English Session filters and actions visible at 1280 pixels", async ({ page, request }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await openConsole(page, request, "sessions");
  await expect(page.getByRole("table", { name: "Session log" })).toBeVisible();
  for (const filter of ["Project", "Agent", "Environment"]) {
    await expect(page.getByRole("combobox", { name: filter, exact: true })).toBeInViewport();
  }
  await expect(page.getByRole("radio", { name: /^Waiting for caller/ })).toBeInViewport();
  expect(await page.locator(".session-log-page").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.locator(".session-log-page .console-page-body").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  const frame = page.locator(".table-frame").filter({ has: page.getByRole("table", { name: "Session log" }) });
  expect(await frame.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await expect(page.getByRole("button", { name: /^Delete Session / }).first()).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath("session-log-en-1280.png") });
});

test("names the E2B backend consistently and explains its two count sources", async ({ page, request }) => {
  await openConsole(page, request, "system", { sandbox: "e2b" });
  await page.getByRole("button", { name: "Change on the Sandbox backend page" }).click();
  await expect(page.getByRole("heading", { name: "Sandbox backend", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sandbox metrics", exact: true }).click();
  await expect(page.getByText(/These sources have different coverage and refresh separately/)).toBeVisible();
});
