import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("reads classified failures and separate Item receipts without execution writes", async ({ page, request }) => {
  await page.route("**/core/v1/projects/*/sessions/*/diagnostics", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    if (data.failure) data.failure = { ...data.failure, code: "authentication_error", params: {} };
    await route.fulfill({ response, json: data });
  });
  await page.route("**/core/v1/projects/*/sessions/*/turns/*/diagnostics", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    if (data.failure) data.failure = { ...data.failure, code: "authentication_error", params: {} };
    data.items = data.items.map((item: object) => ({ ...item, started_at: "2026-09-28T09:00:00Z", completed_at: "2026-09-28T09:00:01.250Z", observed_duration_ms: 1250 }));
    await route.fulfill({ response, json: data });
  });
  await openConsole(page, request, "sessions");
  await page.getByRole("radio", { name: /^Failed/ }).click();
  const row = page.getByRole("row").filter({ hasText: "The model provider rejected authentication" }).first();
  await row.getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByLabel("Session facts")).toContainText("Check its API key");
  await page.getByRole("radio", { name: "Trace", exact: true }).click();
  await page.locator(".trace-ledger-row-tools").first().click();
  await page.getByRole("tab", { name: "Timing", exact: true }).click();
  await expect(page.getByLabel("Core receipt times")).toContainText("1.3 s");
  await expect(page.locator(".trace-detail-timing")).toContainText("Tool-reported duration");
  await page.screenshot({ path: test.info().outputPath("diagnostics-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: test.info().outputPath("diagnostics-mobile.png") });
  expect(await writes(request)).toEqual([]);
});

test("shows diagnostics failure honestly and can retry in Chinese", async ({ page, request }) => {
  let failed = true;
  await page.route("**/core/v1/projects/*/sessions/*/diagnostics", async (route) => {
    if (failed) return route.fulfill({ status: 503, json: { error: { code: "internal_error", message: "RAW_SENTINEL", type: "server_error", param: null } } });
    const response = await route.fetch();
    const data = await response.json();
    if (data.failure) data.failure = { ...data.failure, code: "authentication_error", params: {} };
    await route.fulfill({ response, json: data });
  });
  await openConsole(page, request, "sessions");
  await page.getByRole("radio", { name: /^Failed/ }).click();
  const row = page.getByRole("row").filter({ hasText: "Failure details unavailable" }).first();
  await row.getByRole("button", { name: /^Open Session / }).click();
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  await expect(page.getByLabel("Session 信息")).toContainText("失败详情不可用");
  await expect(page.locator("body")).not.toContainText("RAW_SENTINEL");
  failed = false;
  await page.getByLabel("Session 信息").getByRole("button", { name: "失败详情", exact: true }).click();
  await page.getByLabel("Session 信息").getByRole("button", { name: "重试诊断读取", exact: true }).click();
  await expect(page.getByLabel("Session 信息")).toContainText("模型服务认证失败");
  expect(await writes(request)).toEqual([]);
});

test("connection reads govern host completion and bound-key guidance", async ({ page, request }) => {
  let connected = false;
  let revoked = false;
  let unavailable = false;
  const keyId = "a0000000-0000-4000-8000-000000000001";
  await page.route("**/core/v1/projects/*/environments/*/executor-credentials", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    if (unavailable) return route.fulfill({ status: 503, json: { error: { code: "internal_error", message: "Unreadable connection", type: "server_error", param: null } } });
    await route.fulfill({ json: {
      data: [{ key_id: keyId, created_at: "2026-09-28T08:00:00Z", revoked_at: revoked ? "2026-09-28T09:01:00Z" : null }],
      connection: { status: connected ? "connected" : "disconnected", bound_key_id: keyId, enrolled_at: "2026-09-28T08:00:01Z" },
    } });
  });
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByRole("region", { name: "Host connection", exact: true })).toContainText("Disconnected");
  await expect(page.getByText("Host connected", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Rotate bound credential", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Rotate credential / })).toHaveCount(0);
  revoked = true;
  await expect(page.getByRole("region", { name: "Host connection", exact: true })).toContainText("Bound credential revoked", { timeout: 10_000 });
  await expect(page.getByRole("button", { name: "Rotate bound credential", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Rotate to restore", exact: true })).toBeVisible();
  revoked = false;
  connected = true;
  await expect(page.getByRole("region", { name: "Host connection", exact: true })).toContainText("Connected", { timeout: 10_000 });
  await expect(page.getByText("Host connected", { exact: true })).toBeVisible();
  unavailable = true;
  await expect(page.getByRole("region", { name: "Host connection", exact: true })).toContainText("Unknown", { timeout: 10_000 });
  await expect(page.getByText("Host connected", { exact: true })).toHaveCount(0);
  expect(await writes(request)).toEqual([]);
});
