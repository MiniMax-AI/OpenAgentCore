import { expect, test } from "@playwright/test";

import { expectManagementBoundary, failNext, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("creates a project, shows a new key once, revokes it and archives the project", async ({ page, request }) => {
  await openConsole(page, request, "projects");
  await page.getByRole("button", { name: "Create project" }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("Acceptance");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Acceptance", level: 1 })).toBeVisible();

  await page.getByRole("button", { name: "Issue key" }).first().click();
  await page.getByRole("dialog").getByLabel("Key name").fill("ci");
  await page.getByRole("dialog").getByRole("button", { name: "Issue key" }).click();
  const issued = page.getByRole("dialog", { name: "Key issued" });
  await expect(issued.getByLabel("New key ci")).toHaveValue(/fixture-secret/);
  // How to call Core with it: the official SDK's variables, set to the API base URL and this key.
  const key = await issued.getByLabel("New key ci").inputValue();
  const call = issued.getByRole("region", { name: "How to call" });
  await expect(call.getByLabel("Shell", { exact: true })).toHaveText(`export OPENAI_BASE_URL=https://core.example.com/v1\nexport OPENAI_API_KEY=${key}`);
  await expect(call.getByLabel("curl", { exact: true })).toContainText('curl "$OPENAI_BASE_URL/agents"');
  await expect(call.getByLabel("curl", { exact: true })).toContainText('curl "$OPENAI_BASE_URL/agents/sessions"');
  await expect(call.getByLabel("Python", { exact: true })).toContainText("pip install openai==3.13.0");
  await issued.getByRole("button", { name: "I've saved this key" }).click();
  await expect(page.getByRole("table", { name: "Keys of Acceptance" })).toContainText("ci");
  await expect(page.getByLabel("New key ci")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }))).not.toContain("fixture-secret");

  await page.getByRole("button", { name: "Revoke key ci" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke" }).click();
  await expect(page.getByRole("table", { name: "Keys of Acceptance" })).toContainText("Revoked");

  await page.getByRole("button", { name: "Archive Acceptance" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Archive" }).click();
  await expect(page.getByText("Archived").first()).toBeVisible();
});

test("archives a project with active keys only once its name is typed", async ({ page, request }) => {
  await openConsole(page, request, "projects?id=proj_7f3a91c2");
  await expect(page.getByRole("region", { name: /^Keys/ }).getByRole("heading")).toHaveText("Keys 3 active · 1 revoked");
  await page.getByRole("button", { name: "Archive Production" }).click();
  const dialog = page.getByRole("dialog", { name: "Archive project" });
  await expect(dialog).toContainText("This can't be undone");
  await expect(dialog).toContainText("Its 3 active keys are revoked at once");
  const archive = dialog.getByRole("button", { name: "Archive" });
  await expect(archive).toBeDisabled();
  await dialog.getByLabel("Type Production to confirm").fill("Production");
  await archive.click();
  await expect(page.getByText("Archived").first()).toBeVisible();
});

test("reports an unconfirmed key issue and never replays it", async ({ page, request }) => {
  await openConsole(page, request, "projects");
  await page.getByRole("button", { name: "Open Production" }).click();
  await failNext(request, { method: "POST", path: "/keys", status: 502 });
  await page.getByRole("button", { name: "Issue key" }).first().click();
  await page.getByRole("dialog").getByLabel("Key name").fill("deploy-bot");
  await page.getByRole("dialog").getByRole("button", { name: "Issue key" }).click();
  await expect(page.getByRole("dialog")).toContainText("Core did not confirm the result.");
  await page.waitForTimeout(1_000);
  expect((await writes(request)).filter((entry) => entry.endsWith("/keys"))).toHaveLength(1);
});
