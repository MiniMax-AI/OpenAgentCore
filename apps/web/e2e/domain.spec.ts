import { expect, test, type APIRequestContext } from "@playwright/test";
import { openConsole } from "./console";

const fixture = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
const endpoint = "**/console/installation/domain";
const setDomain = (request: APIRequestContext, data: Record<string, unknown>) => request.post(`${fixture}/__fixture/domain`, { data });

test("System opens domain setup; an apply polls verified readiness", async ({ page, request }, info) => {
  await openConsole(page, request, "system", { installation: "local" });
  await page.getByRole("button", { name: "Configure domain and HTTPS", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Domain and HTTPS", exact: true })).toBeVisible();
  await page.getByLabel("Domain", { exact: true }).fill("https://core.example.com");
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled();
  await page.getByLabel("Domain", { exact: true }).fill("core.example.com");
  await page.screenshot({ path: info.outputPath("domain-before-apply.png"), fullPage: true });
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.getByText("Checking the domain…", { exact: true })).toBeVisible();
  await expect(page.getByText("HTTPS is ready.", { exact: true })).toHaveCount(0);
  await setDomain(request, { state: "ready", public_url: "https://core.example.com" });
  await expect(page.getByText("HTTPS is ready.", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open https://core.example.com" })).toHaveAttribute("href", "https://core.example.com");
});

test("an existing address requires an explicit confirmed write", async ({ page, request }) => {
  await openConsole(page, request, "system");
  await setDomain(request, { state: "ready", public_url: "https://old.example.com", target_url: "https://old.example.com" });
  await page.getByRole("button", { name: "Configure domain and HTTPS", exact: true }).click();
  await page.getByLabel("Domain", { exact: true }).fill("new.example.com");
  const writes: unknown[] = [];
  page.on("request", (req) => { if (req.method() === "POST" && req.url().endsWith("/console/installation/domain")) writes.push(req.postDataJSON()); });
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("https://new.example.com");
  expect(writes).toEqual([{ hostname: "new.example.com" }]);
  await dialog.getByRole("button", { name: "Change address", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(writes).toEqual([{ hostname: "new.example.com" }, { hostname: "new.example.com", confirm_public_url_change: "https://new.example.com" }]);
});

test("an unconfirmed write stops retries until status is read again", async ({ page, request }) => {
  await openConsole(page, request, "system");
  await setDomain(request, { state: "ready", public_url: "https://old.example.com", target_url: "https://old.example.com" });
  await page.getByRole("button", { name: "Configure domain and HTTPS", exact: true }).click();
  let count = 0;
  await page.route(endpoint, (route) => { if (route.request().method() === "POST") { count++; return route.abort(); } return route.continue(); });
  await page.getByLabel("Domain", { exact: true }).fill("core.example.com");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.getByText("The request was not confirmed. Refresh the status before trying again.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled();
  expect(count).toBe(1);
  await expect(page.getByText("HTTPS is ready.", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Open https://core.example.com" })).toBeVisible();
  await page.getByRole("button", { name: "Refresh domain status", exact: true }).click();
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeEnabled();
});

test("a console restart preserves the new-address link on the login screen", async ({ page, request }) => {
  await openConsole(page, request, "system?id=domain");
  await page.getByLabel("Domain", { exact: true }).fill("core.example.com");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.getByText("Checking the domain…", { exact: true })).toBeVisible();
  await page.route("**/console/auth", (route) => route.fulfill({ json: { mode: "login" } }));
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.getByRole("heading", { name: "Sign in to OpenAgentCore" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open https://core.example.com" })).toBeVisible();
});

test("unsupported and failed states preserve actionable facts", async ({ page, request }) => {
  await openConsole(page, request, "system");
  await setDomain(request, { supported: false, message: "This installation is managed externally." });
  await page.getByRole("button", { name: "Configure domain and HTTPS", exact: true }).click();
  await expect(page.getByText("Domain setup is not available for this installation.", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Show details" }).click();
  await expect(page.locator("[role=tooltip]")).toHaveText("This installation is managed externally.");
  await expect(page.getByLabel("Domain", { exact: true })).toHaveCount(0);
  await setDomain(request, { supported: true, state: "failed", target_url: "https://core.example.com", message: "DNS does not point to this server." });
  await page.reload();
  await expect(page.getByRole("alert")).toContainText("Domain setup failed.");
  await page.getByRole("button", { name: "Show details" }).click();
  await expect(page.locator("[role=tooltip]")).toHaveText("DNS does not point to this server.");
  await expect(page.getByRole("button", { name: "Retry setup" })).toBeEnabled();
});

test("a confirmed failed setup clears the login handoff", async ({ page, request }) => {
  await openConsole(page, request, "system?id=domain");
  await page.getByLabel("Domain", { exact: true }).fill("core.example.com");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.getByRole("link", { name: "Open https://core.example.com" })).toBeVisible();
  await setDomain(request, { state: "failed", message: "DNS validation failed." });
  await expect(page.getByRole("alert")).toContainText("Domain setup failed.");
  await page.route("**/console/auth", (route) => route.fulfill({ json: { mode: "login" } }));
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.getByRole("heading", { name: "Sign in to OpenAgentCore" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open https://core.example.com" })).toHaveCount(0);
});
