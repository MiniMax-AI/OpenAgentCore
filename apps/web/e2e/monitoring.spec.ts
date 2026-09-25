import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("shows the deployment's health on Overview and each monitor page", async ({ page, request }) => {
  await openConsole(page, request, "overview");
  await expect(page.getByRole("article").filter({ hasText: "Service status" })).toContainText("Degraded");
  await page.getByRole("button", { name: /^edge-03, Offline/ }).click();
  await expect(page.getByRole("dialog", { name: "edge-03" })).toContainText("Available memory");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Core metrics" }).click();
  const core = page.getByLabel("Core summary");
  await expect(core).toContainText("Execution slots");
  // An unmeasured figure is missing, not zero.
  await expect(core.locator(".kpi").filter({ hasText: /^Memory/ }).locator("dd")).toHaveText("—");
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

test("opens a node and a sandbox in dialogs from Sandbox metrics", async ({ page, request }) => {
  await openConsole(page, request, "sandbox-metrics");
  await page.getByRole("button", { name: "Show core-01" }).click();
  const node = page.getByRole("dialog", { name: "core-01" });
  await expect(node.getByLabel("Node figures")).toContainText("Free memory");
  await expect(node).toContainText("Hosted sandboxes on this node");
  await node.getByRole("button", { name: "Close dialog" }).click();

  await page.getByRole("button", { name: /^Show sandbox of / }).first().click();
  const sandbox = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "Open Session" }) });
  await expect(sandbox.getByLabel("Sandbox")).toContainText("Node");
  await sandbox.getByRole("button", { name: "Open Session" }).click();
  await expect(page.getByRole("list", { name: "Conversation" }).or(page.getByText("No Items yet"))).toBeVisible();
});
