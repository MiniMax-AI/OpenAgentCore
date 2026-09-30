import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";

import { archiveProject, expectManagementBoundary, openConsole } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

/** The self-hosted installer digest the fixture console reports. */

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
  await expect(page.locator(".status-with-help").filter({ hasText: /^Provider not ready/ }).getByRole("button", { name: "Docker limits unsupported", exact: true })).toBeVisible();
});

test("opens a Session's conversation from the Session log, read-only", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Churn forecaster" }).first().getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByRole("list", { name: "Conversation" })).toBeVisible();
  await expect(page.locator(".chat-row.user").first()).toBeVisible();
  await expect(page.getByRole("textbox")).toHaveCount(0);
});

test("keeps a failed Session's reason in sight in the Session log and on its page", async ({ page, request }) => {
  const reasons = /Execution could not complete\. No specific cause was reported\./;
  await page.setViewportSize({ width: 1280, height: 800 });
  await openConsole(page, request, "sessions");
  await page.getByRole("radio", { name: /^Failed/ }).click();
  const row = page.getByRole("row").filter({ hasText: reasons }).first();
  const reason = (await row.getByText(reasons).textContent())!;
  await expect(row.getByText(reason)).toHaveAttribute("title", reason);
  // The reason never widens the table: its last column, Delete, stays in view without scrolling sideways.
  await expect(row.getByRole("button", { name: /^Delete Session / })).toBeInViewport();
  await row.getByRole("button", { name: /^Open Session / }).click();
  await expect(page.getByLabel("Session facts")).toContainText(reason);
  await page.getByRole("button", { name: /^Jump to (the|a) failed Turn/ }).click();
  await expect(page.locator("li.chat-turn:focus")).toContainText("Failed");
});

test("opens an Agent's Sessions from its failed Turns on Agent metrics", async ({ page, request }) => {
  await openConsole(page, request, "agent-metrics");
  const link = /^\d+ failed Turns? — open .+'s Sessions$/;
  const row = page.getByRole("row").filter({ has: page.getByRole("button", { name: link }) }).first();
  const failed = row.getByRole("button", { name: link });
  await expect(failed).toHaveAttribute("title", link);
  const agent = (await row.getByRole("rowheader").innerText()).trim();
  const project = (await row.getByRole("cell").first().innerText()).trim();
  await failed.click();
  await expect(page.getByRole("heading", { name: "Session log", level: 1 })).toBeVisible();
  // The log's own project and Agent filters, with every status.
  await expect(page.getByRole("combobox", { name: "Project", exact: true })).toHaveText(project);
  await expect(page.getByRole("combobox", { name: "Agent", exact: true })).toHaveText(agent);
  await expect(page.getByRole("radio", { name: /^All/ })).toBeChecked();
  await expect(page.getByRole("searchbox")).toHaveValue("");
  const sessions = page.getByRole("table", { name: "Session log" }).getByRole("row");
  await expect(sessions.nth(1)).toContainText(agent);
  await expect(sessions.filter({ hasNotText: agent })).toHaveCount(1);
});

test("shows a self-hosted Session's install command, issues its credential once, and revokes and restores it", async ({ page, request }) => {
  await openConsole(page, request, "sessions");
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  const section = page.getByRole("region", { name: "Executor credentials" });
  await expect(section).toContainText("No executor credentials yet");
  const environmentId = await page.getByLabel("Session facts").locator("div").filter({ hasText: /^Environment/ }).locator("code").getAttribute("title");

  // Web displays Core-provided commands with short-lived installation authority.
  const install = section.getByRole("region", { name: "Connect a host" });
  await expect(install.getByLabel("Executor install command").locator("pre")).toHaveText("bash fixture-bootstrap --authorization fixture-short-lived");
  await expect(install.getByRole("link")).toHaveAttribute("href", /docs\/getting-started\/self-hosted.md$/);
  await install.getByRole("combobox", { name: "Host platform" }).click();
  await page.getByRole("option", { name: "Windows · PowerShell" }).click();
  await expect(install.locator("pre")).toContainText("fixture-bootstrap.ps1 -Authorization fixture-short-lived");
  await install.screenshot({ path: test.info().outputPath("native-host.png") });

  await section.getByRole("button", { name: "Issue credential" }).click();
  const issued = page.getByRole("dialog", { name: "Executor credential" });
  await expect(issued).toContainText("shown only once");
  await expect(issued).toContainText("Download and privately save the credential file before choosing Done.");
  await expect(issued.getByLabel("Executor install command")).toHaveCount(0);
  await expect(issued.getByRole("button", { name: "Download credential file" })).toHaveClass(/\bprimary\b/);
  await expect(issued.getByLabel("Executor credential file")).toHaveText(new RegExp(`^\\{"key_id":"[0-9a-f-]{36}","environment_id":"${environmentId}","executor_token":"exec_fixture_\\d+"\\}$`));
  await expect(issued).toContainText("Enter its absolute path during interactive installation, or use --credential-file <absolute path>");
  const download = page.waitForEvent("download");
  await issued.getByRole("button", { name: "Download credential file" }).click();
  expect((await download).suggestedFilename()).toMatch(/^executor-credential-[0-9a-f]{8}\.json$/);
  // The file is the same line, ended by a newline.
  expect(readFileSync(await (await download).path(), "utf8")).toMatch(/^\{[^\n]*"executor_token":"exec_fixture_\d+"\}\n$/);
  // Dismissing the dialog keeps the credential on the page; only Done forgets it.
  await page.keyboard.press("Escape");
  const pending = section.getByRole("region", { name: "Executor credential", exact: true });
  await expect(pending.getByLabel("Executor credential file")).toContainText("exec_fixture_");
  await pending.getByRole("button", { name: "Done" }).click();
  await expect(page.getByLabel("Executor credential file")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.stringify({ ...window.localStorage, ...window.sessionStorage }))).not.toContain("exec_fixture_");
  expect(page.url()).not.toContain("exec_fixture_");

  const credentials = section.getByRole("table", { name: "Executor credentials" });
  await expect(credentials).toContainText("Active");
  await credentials.getByRole("button", { name: /^Revoke credential / }).click();
  await page.getByRole("dialog", { name: "Revoke credential?" }).getByRole("button", { name: "Revoke" }).click();
  await expect(credentials).toContainText("Revoked");
  await expect(credentials.getByRole("button", { name: /^Revoke credential / })).toHaveCount(0);
  // Rotating the revoked credential restores it with a new secret: its host reconnects only with the same key ID.
  await expect(credentials.getByRole("button", { name: /^Rotate credential / })).toHaveCount(0);
  await credentials.getByRole("button", { name: /^Restore credential / }).click();
  const rotation = page.getByRole("dialog", { name: "Restore credential?" });
  await expect(rotation).toContainText("generating a new secret for the same credential");
  await expect(rotation).toContainText("Run the installed daemon’s stop command, replace the configured credential file on the host, then run start to reconnect.");
  await rotation.getByRole("button", { name: "Restore" }).click();
  const restored = page.getByRole("dialog", { name: "Executor credential" });
  await expect(restored.getByLabel("Executor credential file")).toContainText("exec_fixture_2");
  await restored.getByRole("button", { name: "Done" }).click();
  await expect(credentials).toContainText("Active");
  await expect(credentials).not.toContainText("Revoked");
  await expect(credentials.getByRole("button", { name: /^Rotate credential / })).toBeVisible();
  await expect(credentials.getByRole("button", { name: /^Restore credential / })).toHaveCount(0);

  // Archived meanwhile: Core refuses the issuance and the console stops offering it.
  await archiveProject(request, new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("project")!);
  await section.getByRole("button", { name: "Issue credential" }).click();
  await expect(section).toContainText("This project is archived");
  await expect(section.getByRole("button", { name: "Issue credential" })).toHaveCount(0);
  await expect(credentials.getByRole("button", { name: /^Rotate credential / })).toHaveCount(0);
  await expect(credentials.getByRole("button", { name: /^Revoke credential / })).toBeVisible();
  // Archiving removes the short-lived installation command, while existing credentials remain manageable.
  await expect(install).toContainText("This project is archived. New installation authorizations are unavailable.");
  await expect(install.getByLabel("Executor install command")).toHaveCount(0);
});

test("displays Core's installation command for a loopback Core", async ({ page, request }) => {
  await openConsole(page, request, "sessions", { installation: "local" });
  const installationResponse = page.waitForResponse((response) => response.url().includes("/environments/") && response.url().endsWith("/installation"));
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  const response = await installationResponse;
  expect(response.ok()).toBe(true);
  const installation = await response.json();
  expect(installation.status).toBe("available");
  const install = page.getByRole("region", { name: "Connect a host" });
  // Connection details belong to Core's authorization, not a command rebuilt in Web.
  await expect(install.getByLabel("Executor install command").locator("pre")).toHaveText(installation.commands.posix);
});

test("displays Core's installation command without console installer assets", async ({ page, request }) => {
  await openConsole(page, request, "sessions", { installers: "none" });
  const installationResponse = page.waitForResponse((response) => response.url().includes("/environments/") && response.url().endsWith("/installation"));
  await page.getByRole("row").filter({ hasText: "Self-hosted" }).first().getByRole("button", { name: /^Open Session / }).click();
  const response = await installationResponse;
  expect(response.ok()).toBe(true);
  const installation = await response.json();
  expect(installation.status).toBe("available");
  const section = page.getByRole("region", { name: "Executor credentials" });
  await expect(section).toContainText("No executor credentials yet");
  await expect(section.getByLabel("Executor install command").locator("pre")).toHaveText(installation.commands.posix);
  await expect(section.getByRole("button", { name: "Issue credential" })).toBeVisible();
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
  await page.getByRole("button", { name: "Refresh", exact: true }).first().click();
  const credentials = section.getByRole("table", { name: "Executor credentials" });
  await credentials.getByRole("button", { name: /^Rotate credential / }).click();
  // Rotating an active credential disconnects its host until the command is rerun with the new one.
  await expect(page.getByRole("dialog", { name: "Rotate credential?" })).toContainText("The host disconnects. Run the installed daemon’s stop command, replace its configured credential file, then run start.");
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

  await page.getByRole("navigation").getByRole("button", { name: "Nodes", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Nodes", level: 1 })).toBeVisible();
  await expect(page.getByText("E2B runs sandboxes in its cloud. There are no nodes to manage.")).toBeVisible();
});
