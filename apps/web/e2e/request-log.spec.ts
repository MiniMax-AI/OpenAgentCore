import { expect, test } from "@playwright/test";
import { expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));
const id = (n: number) => `req_${String(n).padStart(32, "0")}`;

test("inspects request arrival and HTTP outcomes without confusing Turn completion", async ({ page, request }) => {
  const reads: string[] = [];
  page.on("request", (request) => { if (request.url().includes("/request-logs")) reads.push(request.url()); });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.setViewportSize({ width: 1280, height: 800 });
  await openConsole(page, request, "request-log");
  await expect(page.getByRole("heading", { name: "Request log", exact: true })).toBeVisible();
  await expect(page.getByText("HTTP completion does not mean the Agent Turn has finished.")).toBeVisible();
  const table = page.getByRole("table", { name: "Request log" });
  await expect(table.getByRole("row")).toHaveCount(5);
  const accepted = table.getByRole("row").filter({ hasText: "POST" });
  await expect(accepted).toContainText("202");
  await expect(accepted).toContainText("Completed");
  await expect(accepted.getByRole("cell").last()).toBeInViewport();
  await table.getByRole("button", { name: `Open request ${id(4)}`, exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Request details" });
  await expect(dialog).toContainText("Elapsed at snapshot");
  await expect(dialog).toContainText("1,024");
  await dialog.getByRole("button", { name: "Copy trace ID" }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("00000000000000000000000000000004");
  await page.keyboard.press("Escape");
  expect(reads).toHaveLength(1);
  await page.getByRole("radio", { name: "Errors", exact: true }).click();
  await expect(table.getByRole("row")).toHaveCount(3);
  await expect(table).toContainText("Aborted");
  await table.getByRole("button", { name: `Open request ${id(3)}`, exact: true }).click();
  await expect(dialog).toContainText("invalid_api_key");
  await page.keyboard.press("Escape");
  await page.getByRole("radio", { name: "Active", exact: true }).click();
  await expect(table.getByRole("row")).toHaveCount(2);
  await expect(table).toContainText("Active");
  await page.getByRole("radio", { name: "All", exact: true }).click();
  await page.getByRole("combobox", { name: "HTTP method" }).click();
  await page.getByRole("option", { name: "POST", exact: true }).click();
  await expect(table.getByRole("row")).toHaveCount(2);
  await expect(table).toContainText("POST");
  await page.getByRole("combobox", { name: "HTTP method" }).click();
  await page.getByRole("option", { name: "All methods", exact: true }).click();
  await page.getByRole("searchbox", { name: "Request ID", exact: true }).fill(id(2));
  await page.getByRole("button", { name: "Find request", exact: true }).click();
  await expect(table.getByRole("row")).toHaveCount(2);
  await expect(table).toContainText("POST");
  await page.getByRole("searchbox").fill("");
  await page.getByRole("button", { name: "Find request", exact: true }).click();
  await page.getByRole("radio", { name: "Administration API", exact: true }).click();
  await expect(table).toContainText("/core/v1/projects");
  await expect(table.getByRole("row")).toHaveCount(2);
});

test("keeps a failed refresh visibly stale and recovers without losing retained requests", async ({ page, request }) => {
  await openConsole(page, request, "request-log");
  const table = page.getByRole("table", { name: "Request log" });
  await expect(table).toBeVisible();
  await page.route("**/core/v1/request-logs?*", (route) => route.fulfill({ status: 503, json: { error: { message: "Unavailable", type: "server_error", code: "unavailable" } } }));
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByText("Some reads failed. Any figures and rows shown cover only data already read; they may be incomplete or out of date.")).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(5);
  await page.unroute("**/core/v1/request-logs?*");
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByText("Some reads failed. Any figures and rows shown cover only data already read; they may be incomplete or out of date.")).toHaveCount(0);
});

test("distinguishes unavailable reads, empty retained results and bounded coverage", async ({ page, request }) => {
  await page.route("**/core/v1/request-logs?*", (route) => route.fulfill({ status: 503, json: { error: { message: "Unavailable", type: "server_error", code: "unavailable" } } }));
  await openConsole(page, request, "request-log");
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.getByText("No retained requests match")).toHaveCount(0);
  await page.unroute("**/core/v1/request-logs?*");
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByRole("table")).toBeVisible();
  await page.route("**/core/v1/request-logs?*", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    await route.fulfill({ json: { ...body, retained_count: 1000, total_matching: 500, has_more: true } });
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByText("Newest 4 shown. Narrow the filters to find another retained request.")).toBeVisible();
  await page.unroute("**/core/v1/request-logs?*");
  await page.getByRole("searchbox").fill(id(9));
  await page.getByRole("button", { name: "Find request", exact: true }).click();
  await expect(page.getByText("No retained requests match")).toBeVisible();
});
