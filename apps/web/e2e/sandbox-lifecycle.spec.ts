import { expect, test } from "@playwright/test";
import { openConsole, expectManagementBoundary, setDeployment, writes } from "./console";

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
  await expect(page.getByRole("heading", { name: "Capacity and suspension" })).toBeVisible();
  await expect(page.getByText("5 minutes", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Capacity and suspension" }).getByRole("columnheader", { name: "Retained / limit" })).toBeVisible();
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


test("edits deployment idle time beside node limits without changing generation", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  const deployment = await setDeployment(request, { suspension: { idle_seconds: 300, retention_seconds: 86400 } });
  await setDeployment(request, { provider: "microsandbox", specification: { ...deployment.specification!, resources: { ...deployment.specification!.resources, root_disk_mib: 8192, environment_disk_mib: 8192 } } });
  await page.getByRole("button", { name: "Refresh sandbox state" }).click();
  const area = page.getByRole("region", { name: "Capacity and suspension" });
  const idle = area.getByLabel("Idle before suspension (seconds)");
  await expect(idle).toHaveValue("300");
  await idle.fill("29");
  await expect(area.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  await expect(area.getByText("Enter a whole number from 30 to 86,400.")).toBeVisible();
  await idle.fill("60");
  const response = page.waitForResponse((response) => response.request().method() === "PATCH" && response.url().endsWith("/sandbox/deployment"));
  await area.getByRole("button", { name: "Save", exact: true }).click();
  const saved = await (await response).json();
  expect(saved.generation).toBe(deployment.generation);
  expect(saved.suspension).toEqual({ idle_seconds: 60, retention_seconds: 86400 });
  await expect(idle).toHaveValue("60");
  await page.reload();
  await expect(idle).toHaveValue("60");
  expect((await writes(request)).filter((write) => write === "PATCH /core/v1/sandbox/deployment")).toHaveLength(1);
});
