import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole } from "./console";

test.afterEach(async ({ request, page }) => expectManagementBoundary(request, page));

test("shows the deployment's health on Overview and each monitor page", async ({ page, request }) => {
  await openConsole(page, request, "overview");
  await expect(page.getByRole("article").filter({ hasText: "Service status" })).toContainText("Degraded");
  await page.getByRole("button", { name: /^edge-03, Offline/ }).click();
  await expect(page.getByRole("dialog", { name: "edge-03" })).toContainText("Available memory");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Core metrics" }).click();
  await expect(page.getByLabel("Core summary")).toContainText("Execution slots");
  await page.getByRole("button", { name: "Agent metrics" }).click();
  await expect(page.getByLabel("Agent run summary")).toContainText("Requests");
  await page.getByRole("button", { name: "Sandbox metrics" }).click();
  await expect(page.getByRole("table").first()).toContainText("core-01");
});

test("opens a Session's conversation from the Session log, read-only", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Churn forecaster" }).first().getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.locator(".chat-row.user").first()).toBeVisible();
  await expect(page.getByRole("textbox")).toHaveCount(0);
});

test('Core metrics keep missing measurements unknown and make a refresh failure visible', async ({ page, request }) => {
  await openConsole(page, request, 'core-metrics');
  await expect(page.getByLabel('Core summary')).toBeVisible();
  await request.post(`http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18611}/__fixture/metrics-unknown`);
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  const values = page.getByLabel('Core summary').locator('.kpi-value');
  await expect(values).toHaveCount(5);
  for (const value of await values.all()) await expect(value).toHaveText('—');
  await failNext(request, { method: 'GET', path: '/core-metrics', status: 503, repeat: true });
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Refresh failed');
  for (const value of await values.all()) await expect(value).toHaveText('—');
});
