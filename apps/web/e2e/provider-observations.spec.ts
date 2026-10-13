import { expect, test } from "@playwright/test";
import { expectManagementBoundary, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

test("default provider observations are optional details and recover only on Core success", async ({ page, request }) => {
  let observation = { last_used_at: null as string | null, last_error_code: null as string | null, last_error_at: null as string | null };
  let fail = false;
  await page.route("**/core/v1/harnesses", async (route) => {
    if (fail) return route.fulfill({ status: 503, json: { error: { code: "internal_error", message: "Read unavailable", type: "server_error", param: null } } });
    const response = await route.fetch();
    const data = await response.json();
    for (const harness of data.data) if (harness.model_configuration) Object.assign(harness.model_configuration, observation);
    await route.fulfill({ response, json: data });
  });
  await openConsole(page, request, "settings?id=models");
  const card = page.getByRole("article", { name: "Codex", exact: true });
  await expect(card).not.toContainText("Last successful use");
  await card.getByRole("button", { name: "Usage details", exact: true }).click();
  const details = page.getByRole("dialog", { name: "Codex usage details", exact: true });
  await expect(details).toContainText("Not recorded");
  await details.getByRole("button", { name: "Close dialog", exact: true }).click();
  observation = { last_used_at: null, last_error_code: "authentication_error", last_error_at: "2026-09-28T08:01:00Z" };
  await page.getByRole("button", { name: "Refresh system configuration", exact: true }).click();
  await expect(card).toContainText("Recent error");
  await card.getByRole("button", { name: "Usage details", exact: true }).click();
  await expect(details).toContainText("The model provider rejected authentication");
  await expect(details).not.toContainText("authentication_error");
  await details.getByRole("button", { name: "Close dialog", exact: true }).click();
  observation.last_used_at = "2026-09-28T08:02:00Z";
  await page.getByRole("button", { name: "Refresh system configuration", exact: true }).click();
  await expect(card).not.toContainText("Recent error");
  await card.getByRole("button", { name: "Usage details", exact: true }).click();
  await expect(details).toContainText("None recorded");
  await expect(details).not.toContainText("The model provider rejected authentication");
  await details.getByRole("button", { name: "Close dialog", exact: true }).click();
  fail = true;
  await page.getByRole("button", { name: "Refresh system configuration", exact: true }).click();
  await expect(card).toContainText("Observation unconfirmed");
  expect(await writes(request)).toEqual([]);
});
