import { expect, test } from "@playwright/test";

import { archiveProject, expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("shows the deployment's health on Overview and each monitor page", async ({ page, request }) => {
  await openConsole(page, request, "overview");
  await expect(page.getByRole("article").filter({ hasText: "Service status" })).toContainText("Degraded");
  await page.getByRole("button", { name: /^edge-03, Offline/ }).click();
  await expect(page.getByRole("dialog", { name: "edge-03" })).toContainText("Available memory");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Core metrics" }).click();
  const core = page.getByLabel("Core summary");
  await expect(core).toContainText("Execution concurrency");
  // An unmeasured figure is missing, not zero.
  await expect(core.locator(".kpi").filter({ hasText: /^Memory/ }).locator("dd")).toHaveText("—");
  await page.getByRole("button", { name: "Agent metrics" }).click();
  await expect(page.getByLabel("Agent run summary")).toContainText("Requests");
  await page.getByRole("button", { name: "Sandbox metrics" }).click();
  await expect(page.getByRole("table").first()).toContainText("core-01");
  // A degraded node names why its provider is not ready.
  await expect(page.getByRole("button", { name: "Docker limits unsupported" })).toBeVisible();
});

test("opens a Session's conversation from the Session log, read-only", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Churn forecaster" }).first().getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.locator(".chat-row.user").first()).toBeVisible();
  await expect(page.getByRole("textbox")).toHaveCount(0);
});

test("issues an executor credential once on a self-hosted Session and revokes it", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  const section = page.getByRole("region", { name: "Executor credentials" });
  await expect(section).toContainText("No executor credentials yet");

  await section.getByRole("button", { name: "Issue credential" }).click();
  const issued = page.getByRole("dialog", { name: "Executor credential" });
  await expect(issued).toContainText("shown only once");
  await expect(issued.getByLabel("Executor credential file")).toContainText("exec_fixture_");
  const download = page.waitForEvent("download");
  await issued.getByRole("button", { name: "Download credential file" }).click();
  expect((await download).suggestedFilename()).toMatch(/^executor-credential-[0-9a-f]{8}\.json$/);
  // Dismissing the dialog keeps the credential on the page; only Done forgets it.
  await page.keyboard.press("Escape");
  const pending = section.getByRole("region", { name: "Executor credential", exact: true });
  await expect(pending.getByLabel("Executor credential file")).toContainText("exec_fixture_");
  await pending.getByRole("button", { name: "Done" }).click();
  await expect(page.getByLabel("Executor credential file")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }))).not.toContain("exec_fixture_");

  const credentials = section.getByRole("table", { name: "Executor credentials" });
  await expect(credentials).toContainText("Active");
  await credentials.getByRole("button", { name: /^Revoke credential / }).click();
  await page.getByRole("dialog", { name: "Revoke credential?" }).getByRole("button", { name: "Revoke" }).click();
  await expect(credentials).toContainText("Revoked");
  await expect(credentials.getByRole("button", { name: /^Rotate credential / })).toHaveCount(0);

  // Archived meanwhile: Core refuses the issuance and the console stops offering it.
  await archiveProject(request, new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("project")!);
  await section.getByRole("button", { name: "Issue credential" }).click();
  await expect(section).toContainText("This project is archived");
  await expect(section.getByRole("button", { name: "Issue credential" })).toHaveCount(0);
  await expect(credentials).toContainText("Revoked");
});

test("issues a new executor credential after the unanswered one was rotated from its row", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  const section = page.getByRole("region", { name: "Executor credentials" });
  await expect(section).toContainText("No executor credentials yet");
  const issuance = () => page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith("/executor-credentials"));

  // Core issues the credential, but its answer never reaches the console.
  await page.route("**/executor-credentials", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await route.fetch();
    await route.abort();
  });
  const unanswered = issuance();
  await section.getByRole("button", { name: "Issue credential" }).click();
  const lost = (await unanswered).postDataJSON().key_id;
  await page.getByRole("dialog", { name: "Couldn't confirm the credential" }).getByRole("button", { name: "Close", exact: true }).click();
  await page.unroute("**/executor-credentials");

  // The page's refresh lists it; the administrator rotates it from its row.
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  const credentials = section.getByRole("table", { name: "Executor credentials" });
  await credentials.getByRole("button", { name: /^Rotate credential / }).click();
  await page.getByRole("dialog", { name: "Rotate credential?" }).getByRole("button", { name: "Rotate" }).click();
  await page.getByRole("dialog", { name: "Executor credential" }).getByRole("button", { name: "Done" }).click();

  // The next Issue is a new credential, not a recovery of the rotated one.
  const next = issuance();
  await section.getByRole("button", { name: "Issue credential" }).click();
  expect((await next).postDataJSON().key_id).not.toBe(lost);
  await expect(page.getByRole("dialog", { name: "Executor credential" }).getByLabel("Executor credential file")).toContainText("exec_fixture_");
});

test("opens a node and a sandbox in dialogs from Sandbox metrics", async ({ page, request }) => {
  await openConsole(page, request, "sandbox-metrics");
  await page.getByRole("button", { name: "Show core-01" }).click();
  const node = page.getByRole("dialog", { name: "core-01" });
  // The machine's own load, from its heartbeats, beside the sandboxes placed on it.
  await expect(node.getByLabel("Node figures")).toContainText("35% of 16 cores");
  await expect(node.getByRole("figure", { name: /^Host CPU/ })).toBeVisible();
  await expect(node).toContainText("Hosted sandboxes on this node");
  await node.getByRole("button", { name: "Close dialog" }).click();

  await page.getByRole("button", { name: /^Show sandbox of / }).first().click();
  const sandbox = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "Open Session" }) });
  await expect(sandbox.getByLabel("Sandbox")).toContainText("Node");
  await sandbox.getByRole("button", { name: "Open Session" }).click();
  await expect(page.getByRole("list", { name: "Conversation" }).or(page.getByText("No Items yet"))).toBeVisible();
});

test("shows E2B's cloud instead of machines", async ({ page, request }) => {
  await openConsole(page, request, "sandbox-metrics", { sandbox: "e2b" });
  await expect(page.getByRole("heading", { name: "E2B cloud" })).toBeVisible();
  // Each sandbox takes the template build's size, disk included.
  await expect(page.getByText("10 GiB disk")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add node" })).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "Node", exact: true })).toHaveCount(0);

  await page.getByRole("navigation").getByRole("button", { name: "Sandbox backend" }).click();
  await expect(page.getByRole("heading", { name: "Sandbox backend", level: 1 })).toBeVisible();
});
