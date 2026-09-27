import { expect, test, type Page } from "@playwright/test";
import { expectManagementBoundary, openConsole, writes } from "./console";

const projectList = /\/core\/v1\/projects(?:\?|$)/;
const sessionLists = /\/core\/v1\/projects\/[^/]+\/sessions(?:\?|$)/;
const reject = { status: 502, contentType: "text/plain", body: "Bad Gateway" };
const failed = (page: Page) => page.locator(".error-state");

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("project failure shows unavailable sections and missing Session counts, then retry recovers", async ({ page, request }) => {
  await page.route(projectList, (route) => route.fulfill(reject));
  await openConsole(page, request);
  for (const name of ["Session activity", "Needs attention", "By project"]) {
    const section = page.getByRole("region", { name, exact: true });
    await expect(section).toContainText("Could not read the data");
    await expect(section.getByRole("button", { name: "Retry" })).toBeVisible();
  }
  await expect(page.locator(".overview-activity .ts-chart-plot")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Needs attention", exact: true })).not.toContainText("Nothing needs attention");
  await page.locator(".app-sidebar").getByRole("button", { name: "Session log", exact: true }).click();
  await expect(page.getByRole("radio", { name: /^All/ })).toContainText("—");
  await expect(failed(page)).toBeVisible();
  await page.unroute(projectList);
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByRole("table", { name: "Session log" })).toBeVisible();
  await expect(page.getByRole("radio", { name: /^All/ })).not.toContainText("—");
});

test("failed Session reads keep Core summary figures but never synthesize an empty activity chart", async ({ page, request }) => {
  await page.route(sessionLists, (route) => route.fulfill(reject));
  await openConsole(page, request);
  await expect(page.getByRole("region", { name: "Session activity", exact: true })).toContainText("Could not read the data");
  await expect(page.locator(".overview-activity .ts-chart-plot")).toHaveCount(0);
  await expect(page.getByRole("article").filter({ hasText: "Running Sessions" }).locator(".metric-tile-value")).not.toHaveText("—");
  await page.locator(".app-sidebar").getByRole("button", { name: "Session log", exact: true }).click();
  await expect(page.getByRole("radio", { name: /^All/ })).toContainText("—");
  await expect(failed(page)).toBeVisible();
  await page.unroute(sessionLists);
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByRole("table", { name: "Session log" })).toBeVisible();
});

test("partial and failed refreshes retain rows with a durable caveat and recover", async ({ page, request }) => {
  let failedProject: string | null = null;
  await page.route(sessionLists, (route) => {
    const id = new URL(route.request().url()).pathname.split("/")[4]!;
    failedProject ??= id;
    return id === failedProject ? route.fulfill(reject) : route.continue();
  });
  await openConsole(page, request, "sessions");
  await expect(page.getByRole("table", { name: "Session log" })).toBeVisible();
  await expect(failed(page)).toContainText("may be incomplete or out of date");
  await expect(page.getByRole("radio", { name: /^All/ })).toContainText("—");
  await page.unroute(sessionLists);
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(failed(page)).toHaveCount(0);
  const rows = await page.getByRole("table", { name: "Session log" }).getByRole("row").count();
  await page.route(projectList, (route) => route.fulfill(reject));
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(failed(page)).toContainText("may be incomplete or out of date");
  await expect(page.getByRole("table", { name: "Session log" }).getByRole("row")).toHaveCount(rows);
  await page.unroute(projectList);
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(failed(page)).toHaveCount(0);
});

test("successful empty reads preserve true zero counts and empty states", async ({ page, request }) => {
  await openConsole(page, request, "overview", { fresh: true });
  await expect(page.locator(".overview-activity .ts-chart-plot").first()).toBeVisible();
  await expect(page.getByRole("article").filter({ hasText: "Running Sessions" }).locator(".metric-tile-sub")).toHaveText("0 total, 0 idle");
  await expect(failed(page)).toHaveCount(0);
  await page.locator(".app-sidebar").getByRole("button", { name: "Session log", exact: true }).click();
  await expect(page.getByRole("radio", { name: /^All/ })).toHaveText(/All\s*0/);
  await expect(failed(page)).toHaveCount(0);
});

test("local-only address has actionable Core instructions on all three pages and blocks Add node", async ({ page, request }) => {
  await openConsole(page, request, "overview", { installation: "local" });
  const notice = page.getByRole("status", { name: "Public address needs attention" });
  await expect(notice).toContainText("/opt/oac/config.json");
  await expect(notice).toContainText("sudo oac apply");
  await expect(page.locator(".getting-started-step").first()).toContainText("To do");
  await expect(page.locator(".getting-started-step").first()).toContainText("Fix the public address");
  await page.getByRole("button", { name: "Nodes", exact: true }).click();
  await expect(notice).toBeVisible();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeDisabled();
  await expect(page.getByText("Add node is unavailable while the public address is local only.")).toBeVisible();
  await page.getByRole("button", { name: "System", exact: true }).click();
  await expect(notice).toBeVisible();
  await expect(notice.getByRole("button", { name: "Copy configuration path" })).toBeVisible();
  await expect(notice.getByRole("button", { name: "Copy apply command" })).toBeVisible();
  expect(await writes(request)).toEqual([]);
});

for (const language of ["en", "zh-CN"] as const) {
  test(`captures localized unavailable and local-only states (${language})`, async ({ page, request }, info) => {
    await page.route(projectList, (route) => route.fulfill(reject));
    await openConsole(page, request, "overview", { installation: "local" });
    if (language === "zh-CN") {
      await page.getByRole("button", { name: "Language and appearance" }).click();
      await page.getByRole("menuitemradio", { name: "简体中文" }).click();
    }
    await expect(page.locator(".overview-activity .error-state")).toContainText(language === "en" ? "Could not read the data" : "无法读取数据");
    await expect(page.locator(".installation-notice")).toContainText(language === "en" ? "Applications, nodes and executors" : "应用、节点和 executor");
    if (language === "zh-CN") await expect(page.locator("body")).not.toContainText("Core request failed");
    await expect(page.getByRole("article").first()).toContainText(language === "en" ? "Down" : "不可用");
    for (const width of [1280, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.screenshot({ path: info.outputPath(`overview-${language}-${width}.png`), fullPage: true });
    }
  });
}

test("Overview preserves partial activity and visibly qualifies a failed project refresh", async ({ page, request }) => {
  let failedProject: string | null = null;
  await page.route(sessionLists, (route) => {
    const id = new URL(route.request().url()).pathname.split("/")[4]!;
    failedProject ??= id;
    return id === failedProject ? route.fulfill(reject) : route.continue();
  });
  await openConsole(page, request);
  const activity = page.getByRole("region", { name: "Session activity", exact: true });
  await expect(activity.locator(".ts-chart-plot")).toBeVisible();
  await expect(activity).toContainText("may be incomplete or out of date");
  await page.unroute(sessionLists);
  await activity.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(activity.locator(".error-state")).toHaveCount(0);
  await page.route(projectList, (route) => route.fulfill(reject));
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(activity).toContainText("may be incomplete or out of date");
  await expect(activity.locator(".ts-chart-plot")).toBeVisible();
  await expect(page.getByRole("region", { name: "By project", exact: true }).getByRole("table")).toBeVisible();
  await page.unroute(projectList);
  await activity.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(activity.locator(".error-state")).toHaveCount(0);
});

test("failed summary and Session refreshes preserve their previous evidence until retry succeeds", async ({ page, request }) => {
  await openConsole(page, request);
  const activity = page.getByRole("region", { name: "Session activity", exact: true });
  const attention = page.getByRole("region", { name: "Needs attention", exact: true });
  const projects = page.getByRole("region", { name: "By project", exact: true });
  await expect(activity.locator(".ts-chart-plot")).toBeVisible();
  await expect(attention.getByRole("table")).toBeVisible();
  const attentionRows = await attention.getByRole("row").count();
  const projectRows = await projects.getByRole("row").count();
  const running = page.getByRole("article").filter({ hasText: "Running Sessions" }).locator(".metric-tile-sub");
  const known = await running.innerText();
  const summary = /\/core\/v1\/summary(?:\?|$)/;
  await page.route(summary, (route) => route.fulfill(reject));
  await page.route(sessionLists, (route) => route.fulfill(reject));
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(activity).toContainText("may be incomplete or out of date");
  await expect(activity.locator(".ts-chart-plot")).toBeVisible();
  await expect(running).toHaveText(known);
  await expect(attention.getByRole("row")).toHaveCount(attentionRows);
  await expect(projects.getByRole("row")).toHaveCount(projectRows);
  await expect(projects).toContainText("may be incomplete or out of date");
  await page.unroute(summary);
  await page.unroute(sessionLists);
  await activity.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(failed(page)).toHaveCount(0);
});

test("installation failure cannot complete onboarding and its retry reveals local-only blockage", async ({ page, request }) => {
  await page.addInitScript(() => window.localStorage.setItem("agents-core-web.getting-started.7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f", "open"));
  await page.route("**/core/v1/installation", (route) => route.fulfill(reject));
  await openConsole(page, request, "overview", { installation: "local" });
  const step = page.locator(".getting-started-step").first();
  await expect(step).toContainText("Unknown");
  await expect(step).toContainText("The installation address could not be read");
  await expect(page.getByText("You're set", { exact: true })).toHaveCount(0);
  await page.unroute("**/core/v1/installation");
  await step.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(step).toContainText("To do");
  await expect(step).toContainText("Fix the public address");
  await expect(page.locator(".installation-notice")).toBeVisible();
});
