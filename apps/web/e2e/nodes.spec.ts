import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole } from "./console";

test.afterEach(async ({ request, page }) => expectManagementBoundary(request, page));

test("prepares a one-time node command and removes a node after confirmation", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await page.getByRole("button", { name: "Add node" }).click();
  const add = page.getByRole("dialog", { name: "Add node" });
  await expect(add.getByLabel("One-time enrollment command")).toHaveValue(/enroll_fixture_/);
  await add.getByRole("button", { name: "Close dialog" }).click();

  await page.getByRole("button", { name: "Remove edge-03" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove node" });
  await confirm.getByRole("button", { name: "Confirm removal" }).click();
  await expect(confirm).toBeHidden();
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).not.toContainText("edge-03");
});


test("failed node reads cannot present the cached hosts as healthy", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).toContainText("edge-03");
  await failNext(request, { method: "GET", path: "/nodes", status: 503, repeat: true });
  await page.reload();
  await expect(page.getByRole("alert").first()).toBeVisible();
  await expect(page.getByRole("table", { name: "Sandbox nodes" })).toHaveCount(0);
});
