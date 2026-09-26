import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("sets up sandboxes with Core's address read-only, never sending it", async ({ page, request }) => {
  const bodies: string[] = [];
  page.on("request", (sent) => { if (sent.url().includes("/core/v1/sandbox/deployment")) bodies.push(sent.postData() ?? ""); });
  await openConsole(page, request, "nodes", { sandbox: "none" });
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "Docker" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  const review = page.getByRole("definition").filter({ hasText: "https://core.example.com" });
  await expect(review).toContainText("Set by public_url in config.json");
  await expect(page.getByRole("textbox", { name: "Core address" })).toHaveCount(0);
  await page.getByRole("button", { name: "Save configuration" }).click();
  // Saved: own machines continue straight to Add node.
  await expect(page.getByRole("dialog", { name: "Add node" })).toBeVisible();
  expect(await writes(request)).toEqual(["POST /core/v1/sandbox/deployment"]);
  expect(bodies.some((entry) => entry.includes("core_url"))).toBe(false);
});

test("explains an E2B rejection in the wizard, with the file to edit and the command to apply it", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { sandbox: "none", installation: "local" });
  await page.getByRole("button", { name: "E2B cloud" }).click();
  await page.getByLabel("E2B API key").fill("fixture-private-key");
  await page.getByLabel("Template build").fill("template:94be54a1-138c-4f30-bc87-b13686272dbe");
  await page.getByRole("button", { name: "Next" }).click();
  const address = page.getByRole("definition").filter({ hasText: "http://127.0.0.1:8091" });
  await expect(address).toContainText("Only the Core machine can reach this address");
  await page.getByRole("button", { name: "Save configuration" }).click();
  const rejection = page.locator(".wizard-rejection");
  await expect(rejection).toContainText("E2B sandboxes reach Core over the internet.");
  await expect(rejection).toContainText("/opt/parsar/config.json");
  await expect(rejection).toContainText("sudo parsar apply");
  // Nothing was saved and nothing is uncertain: no dialog, and the wizard stays on its review.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Review and save" })).toBeVisible();
  // The key is gone from the page; saving again asks for it on the E2B step.
  expect(await page.content()).not.toContain("fixture-private-key");
  await page.getByRole("button", { name: "Enter the key" }).click();
  await expect(page.getByRole("heading", { name: "Connect E2B" })).toBeVisible();
});

test("lists the startup settings on System with where to change them", async ({ page, request }) => {
  await openConsole(page, request, "system");
  const installation = page.getByRole("region", { name: "Installation" });
  await expect(installation).toContainText("https://core.example.com/v1");
  const startup = page.getByRole("region", { name: "Startup settings" });
  await expect(startup).toContainText("Change these in /opt/parsar/config.json, then run sudo parsar apply");
  const settings = startup.getByRole("table", { name: "Startup settings" });
  await expect(settings.getByRole("row", { name: /^log_level/ })).toContainText("debug");
  await expect(settings.getByRole("row", { name: /^listen_address/ })).toContainText("Default");
  await expect(settings.getByRole("row", { name: /^data_dir/ })).toContainText("Fixed after install");
  await expect(settings.getByRole("row", { name: /^core_key/ })).toContainText("Configured");
  await expect(settings.getByRole("row", { name: /^public_url/ })).toContainText("core, web");
});

test("warns on Nodes about a node bound to an old Core address until it is removed", async ({ page, request }) => {
  await openConsole(page, request, "nodes", { installation: "stale" });
  const warning = page.getByRole("status").filter({ hasText: "old Core address" });
  await expect(warning).toHaveText("core-01 is still bound to an old Core address. Remove it and add it again.");
  await page.getByRole("button", { name: "Remove core-01" }).click();
  await page.getByRole("dialog", { name: "Remove node" }).getByRole("button", { name: "Confirm removal" }).click();
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).not.toContainText("core-01");
  await expect(warning).toHaveCount(0);
  // Its host's uninstall confirms the removal at that old address; if it no longer answers, --force skips the check.
  const cleanup = page.getByRole("dialog", { name: "Clean up the host" });
  await cleanup.getByText("Old Core address gone?").click();
  await expect(cleanup).toContainText("core-01 still points at the old Core address https://core-old.example.com.");
  await expect(cleanup.getByLabel("Uninstall command with --force", { exact: true })).toHaveValue(/--uninstall --installation-id '7f3c2a90-5b1e-4c2d-9e3f-0a1b2c3d4e5f' --force\)$/);
});
