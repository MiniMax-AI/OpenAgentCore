import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("keeps Core's reason when it refuses a deletion, then deletes on confirmation", async ({ page, request }) => {
  await openConsole(page, request, "agents");
  await failNext(request, { method: "DELETE", path: "/agents/", status: 409, message: "The Agent has Sessions in progress." });
  await page.getByRole("button", { name: "Delete Code reviewer?" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete Agent?" });
  await dialog.getByRole("button", { name: "Delete Agent" }).click();
  await expect(dialog).toContainText("The Agent has Sessions in progress.");

  await dialog.getByRole("button", { name: "Delete Agent" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: "Open Code reviewer" })).toHaveCount(0);
});

test("goes back to the page a resource was opened from", async ({ page, request }) => {
  await openConsole(page, request, "templates");
  await page.getByRole("button", { name: "Open Report builder" }).click();
  await expect(page.getByRole("heading", { name: "Report builder", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: /^Open skill_.* in Skills$/ }).first().click();
  await expect(page.getByRole("heading", { level: 1 })).not.toHaveText("Report builder");
  await page.getByRole("button", { name: "Back", exact: true }).first().click();
  await expect(page.getByRole("heading", { name: "Report builder", level: 1 })).toBeVisible();
});

test("reads an Agent saved with a model provider and no harness, without its key", async ({ page, request }) => {
  // Production holds Spec drafter, saved with only an x_agents_core.model_provider.
  await openConsole(page, request, "agents?project=proj_7f3a91c2");
  const open = page.getByRole("button", { name: "Open Spec drafter" });
  await expect(open).toBeVisible();
  await expect(page.getByText(/Could not load/)).toHaveCount(0);
  // With one project shown, the cells are model, harness, …
  await expect(page.getByRole("row").filter({ has: open }).getByRole("cell").nth(1)).toHaveText("Core default");

  await open.click();
  await expect(page.getByRole("heading", { name: "Spec drafter", level: 1 })).toBeVisible();
  const provider = page.getByRole("region", { name: "Model provider" });
  for (const text of ["Anthropic Messages", "https://api.minimaxi.com/anthropic", "Configured", "204,800", "65,536"]) await expect(provider).toContainText(text);
  await expect(page.locator("body")).not.toContainText(/api_key|\bsk-/);
});
