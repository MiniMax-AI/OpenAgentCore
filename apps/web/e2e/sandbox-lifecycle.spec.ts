import { expect, test } from "@playwright/test";
import { openConsole, expectManagementBoundary } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("shows automatic suspension policy, retained capacity and elapsed phase time without inventing expiry", async ({ page, request }, testInfo) => {
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    const response = await route.fetch();
    const deployment = await response.json();
    await route.fulfill({ response, json: { ...deployment, provider: "microsandbox", suspension: { idle_seconds: 300, retention_seconds: 86400 }, specification: { ...deployment.specification, resources: { ...deployment.specification.resources, root_disk_mib: 8192, environment_disk_mib: 8192 } } } });
  });
  await page.route("**/core/v1/sandbox/nodes", async (route) => {
    const response = await route.fetch();
    const { data } = await response.json();
    await route.fulfill({ response, json: { data: data.map((node: Record<string, unknown>) => ({ ...node, provider: "microsandbox", retained: Number(node.active) + 2 })) } });
  });
  await openConsole(page, request, "nodes");
  await expect(page.getByRole("heading", { name: "Automatic suspension" })).toBeVisible();
  await expect(page.getByText("5 minutes", { exact: true })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Retained / limit" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("nodes-en.png"), fullPage: true });
  await page.getByRole("button", { name: "Open core-01", exact: true }).click();
  const allocations = page.getByRole("table", { name: "Sandbox allocations" });
  await expect(allocations.getByRole("columnheader", { name: "In this state" })).toBeVisible();
  await expect(allocations).toContainText("Suspended");
  await expect(allocations).not.toContainText("reclaim");
  await expect(allocations).not.toContainText("due");
  await page.getByRole("button", { name: "Sandbox metrics", exact: true }).click();
  await expect(page.getByRole("columnheader", { name: "Recorded compute" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("sandbox-metrics-en.png"), fullPage: true });
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  await expect(page.getByRole("heading", { name: "自动挂起" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "记录的计算状态" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("sandbox-metrics-zh.png"), fullPage: true });
});

test("hides suspension policy when the provider declares no suspension support", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Automatic suspension" })).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "Retained / limit" })).toHaveCount(0);
});
