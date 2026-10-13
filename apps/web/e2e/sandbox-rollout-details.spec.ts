import { expect, test, type Locator } from "@playwright/test";
import { expectManagementBoundary, openConsole, writes } from "./console";

const fact = (scope: Locator, label: string) => scope.locator("dt").filter({ hasText: new RegExp(`^${label}`) }).locator("..").locator("dd");

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("keeps rollout summary brief and opens all Core observations in details", async ({ page, request }) => {
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    const response = await route.fetch();
    const deployment = await response.json();
    await route.fulfill({ response, json: { ...deployment, generation: 4, resources: { allocations: 8, pending: 0 }, rollout: { state: "settled", previous_generation_sandboxes: 8, nodes: { ready: 1, preparing: 0, failed: 2, update_required: 3, unknown: 4 } } } });
  });
  await openConsole(page, request, "settings?id=sandbox");
  const summary = page.getByRole("region", { name: "Configuration rollout", exact: true });
  await expect(summary).toContainText("Needs attention");
  await expect(summary.locator("dl")).toHaveCount(0);
  await expect(summary.getByText("Target generation", { exact: true })).toHaveCount(0);
  const details = summary.getByRole("button", { name: "View rollout details", exact: true });
  await details.click();
  const dialog = page.getByRole("dialog", { name: "Configuration rollout", exact: true });
  await expect(fact(dialog, "Core preparation")).toHaveText("No active preparation");
  await expect(fact(dialog, "Target generation")).toHaveText("4");
  await expect(fact(dialog, "Previous-generation sandboxes")).toHaveText("8");
  await expect(fact(dialog, "Preparation failed")).toHaveText("2");
  await expect(fact(dialog, "Node software incompatible")).toHaveText("3");
  await expect(fact(dialog, "Target readiness unknown")).toHaveText("4");
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(details).toBeFocused();
  expect(await writes(request)).toEqual([]);
});

test("labels unknown target readiness without declaring settled nodes ready", async ({ page, request }) => {
  await page.route("**/core/v1/sandbox/deployment", async (route) => {
    const response = await route.fetch();
    const deployment = await response.json();
    await route.fulfill({ response, json: { ...deployment, rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 1 } } } });
  });
  await openConsole(page, request, "settings?id=sandbox");
  const summary = page.getByRole("region", { name: "Configuration rollout", exact: true });
  await expect(summary).toContainText("Target readiness unknown");
  await expect(summary).not.toContainText("No active preparation");
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  const translated = page.getByRole("region", { name: "配置更新进度", exact: true });
  await expect(translated).toContainText("目标配置就绪状态未知");
  await translated.getByRole("button", { name: "查看详情", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "配置更新进度", exact: true })).toContainText("Core 准备状态");
  expect(await writes(request)).toEqual([]);
});
