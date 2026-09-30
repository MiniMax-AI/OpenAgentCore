import { expect, test, type Page, type TestInfo } from "@playwright/test";
import type { SandboxReset } from "@oac/agents-client";

import { expectManagementBoundary, FIXTURE_CORE_KEY, openConsole, setDeployment, writes } from "./console";

const deploymentURL = "**/core/v1/sandbox/deployment";
const resetURL = "**/core/v1/sandbox/deployment/reset";
const resetPath = "/core/v1/sandbox/deployment/reset";
const unavailable = { status: 503, json: { error: { type: "server_error", code: null, param: null, message: "Deployment read unavailable." } } };
const observedReset = (clear: "auto" | "force" = "auto"): SandboxReset => ({
  clear, requested_at: "2026-09-26T10:00:00Z", deadline_at: "2026-09-26T11:00:00Z",
  forced_at: clear === "force" ? "2026-09-26T11:00:01Z" : null,
  remaining: { busy: 2, idle: 1, cleanup: 1, on_offline_nodes: 2, offline_nodes: [{ node_id: "offline-owned", name: "offline-build-worker", resources: 2 }] },
});
const progress = (page: Page) => page.getByRole("region", { name: "Reset in progress" });
async function openConfiguration(page: Page) {
  await page.getByRole("button", { name: "System", exact: true }).click();
  await page.getByRole("button", { name: "Manage sandbox configuration", exact: true }).click();
}
async function expectEnrollmentBlocked(page: Page) {
  await page.getByRole("button", { name: "Nodes", exact: true }).click();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeDisabled();
  await openConfiguration(page);
}

async function openReset(page: Page) {
  await page.getByRole("button", { name: "Reset deployment", exact: true }).click();
  return page.getByRole("dialog", { name: "Reset sandbox deployment?", exact: true });
}
async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath(`${name}.png`);
  await page.screenshot({ path, fullPage: true });
  await info.attach(name, { path, contentType: "image/png" });
}

test.afterEach(async ({ request }) => {
  await expectManagementBoundary(request);
  expect((await writes(request)).filter((entry) => entry.includes("/maintenance"))).toEqual([]);
});

test("auto reset requires a bounded deadline, then preserves Core progress past that deadline", async ({ page, request }, info) => {
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await openConsole(page, request, "system?id=sandbox");
  await setDeployment(request, { resources: { allocations: 3, pending: 1 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  const dialog = await openReset(page);
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  const entry = page.getByRole("button", { name: "Reset deployment", exact: true });
  await expect(entry).toBeFocused();
  expect(await writes(request)).toEqual([]);
  await entry.press("Enter");
  await expect(dialog).toBeVisible();
  const deadline = dialog.getByLabel(/Wait before forcing \(seconds\)/);
  await expect(deadline).toHaveValue("3600");
  await deadline.fill("299");
  await expect(dialog.getByRole("button", { name: "Reset deployment", exact: true })).toBeDisabled();
  await deadline.fill("86401");
  await expect(dialog.getByRole("button", { name: "Reset deployment", exact: true })).toBeDisabled();
  await deadline.fill("300");
  await expect(dialog).toContainText("Hosted Sessions will be archived permanently.");
  await dialog.getByRole("button", { name: "What reset affects", exact: true }).click();
  await expect(page.locator(".help-tip-popover")).toContainText("Archived Sessions cannot be resumed");
  await dialog.getByRole("button", { name: "What reset affects", exact: true }).click();
  await expect(dialog).toContainText("self-hosted");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await capture(page, info, "reset-confirm-en-light-1280");
  const submitted = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith(resetPath));
  await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
  expect((await submitted).postDataJSON()).toEqual({ expected_generation: 1, clear: "auto", deadline_seconds: 300 });
  await expect(progress(page)).toBeVisible();
  // These timestamps are in the past: the browser still cannot invent force or completion.
  await setDeployment(request, { resources: { allocations: 3, pending: 1 }, reset: observedReset() });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(progress(page)).toContainText("offline-build-worker");
  await expect(progress(page)).toContainText("Busy work can finish until the deadline.");
  await expectEnrollmentBlocked(page);
  await expect(page.getByRole("heading", { name: "Where should sandboxes run?" })).toHaveCount(0);
  await capture(page, info, "reset-progress-en-light-1280");
  expect(await writes(request)).toEqual([`POST ${resetPath}`]);
});

test("escalation and cancellation require explicit confirmation and never undo previous clearing", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await setDeployment(request, { resources: { allocations: 3, pending: 1 }, reset: observedReset() });
  await page.reload();
  await progress(page).getByRole("button", { name: "Force reset now" }).click();
  let confirm = page.getByRole("dialog", { name: "Force reset now?" });
  await confirm.getByRole("button", { name: "Back", exact: true }).click();
  expect(await writes(request)).toEqual([]);
  await progress(page).getByRole("button", { name: "Force reset now" }).click();
  confirm = page.getByRole("dialog", { name: "Force reset now?" });
  const force = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith(resetPath));
  await confirm.getByRole("button", { name: "Force reset now", exact: true }).click();
  expect((await force).postDataJSON()).toEqual({ expected_generation: 1, clear: "force" });
  await expect(progress(page)).toContainText("Remaining hosted Sessions are being archived");
  await expect(progress(page)).toContainText("offline-build-worker");
  await expect(progress(page).getByRole("button", { name: "Force reset now" })).toHaveCount(0);
  await progress(page).getByRole("button", { name: "Cancel reset" }).click();
  confirm = page.getByRole("dialog", { name: "Cancel reset?" });
  await expect(confirm).toContainText("Sessions already archived stay archived");
  const cancel = page.waitForRequest((sent) => sent.method() === "DELETE" && sent.url().includes(resetPath));
  await confirm.getByRole("button", { name: "Cancel reset", exact: true }).click();
  expect(new URL((await cancel).url()).searchParams.get("expected_generation")).toBe("1");
  await expect(progress(page)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([`POST ${resetPath}`, `DELETE ${resetPath}`]);
});

test("force reset requires confirmation and reconfiguration uses the completed generation once", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await setDeployment(request, { resources: { allocations: 1, pending: 0 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  const dialog = await openReset(page);
  await dialog.getByLabel("Force — cancel remaining work now", { exact: true }).check();
  await expect(dialog).toContainText("Force immediately cancels remaining work");
  expect(await writes(request)).toEqual([]);
  await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
  await expect(progress(page)).toContainText("Remaining hosted Sessions are being archived");
  // Simulate the cleanup worker's completed projection, not elapsed browser time.
  await setDeployment(request, { complete_reset: true });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await page.getByRole("button", { name: "Own machines" }).click();
  await page.getByRole("button", { name: "microsandbox Recommended" }).click();
  await page.getByRole("button", { name: /^Standard/ }).click();
  const configured = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith("/sandbox/deployment"));
  await page.getByRole("button", { name: "Save configuration" }).click();
  expect((await configured).postDataJSON()).toMatchObject({ expected_generation: 2, provider: "microsandbox" });
  await expect(page.getByRole("dialog", { name: "Add node" })).toBeVisible();
  expect(await writes(request)).toEqual([`POST ${resetPath}`, "POST /core/v1/sandbox/deployment"]);
});

test("an applied reset with a lost response stays blocked through failed reads and is never replayed", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  await setDeployment(request, { resources: { allocations: 1, pending: 0 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  let failReads = false;
  await page.route(resetURL, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const accepted = await route.fetch(); // Core accepted the mutation, but its response never reached this browser.
    expect(accepted.ok()).toBe(true);
    // Only fail reconciliation reads after acceptance; earlier reads must allow the explicit submission.
    failReads = true;
    await route.abort("failed");
  });
  await page.route(deploymentURL, (route) => failReads ? route.fulfill(unavailable) : route.continue());
  const dialog = await openReset(page);
  await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
  const uncertain = page.getByRole("dialog", { name: "Couldn't confirm the sandbox change" });
  await expect(uncertain).toBeVisible();
  await expect(dialog).toHaveCount(0);
  await uncertain.getByRole("button", { name: "Refresh sandbox state" }).click();
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeDisabled();
  expect(await writes(request)).toEqual([`POST ${resetPath}`]);
  failReads = false;
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  await expect(progress(page)).toContainText("Busy work can finish until the deadline.");
  await expect(progress(page).getByRole("button", { name: "Cancel reset" })).toBeEnabled();
  expect(await writes(request)).toEqual([`POST ${resetPath}`]);
});

test("a stale reset is refused until fresh state is reviewed, with no automatic resubmission", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  await setDeployment(request, { generation: 2, resources: { allocations: 3, pending: 1 } });
  const dialog = await openReset(page);
  const staleSubmission = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith(resetPath));
  const freshRead = page.waitForResponse(async (received) => received.request().method() === "GET"
    && received.url().endsWith("/sandbox/deployment") && received.ok() && (await received.json()).generation === 2);
  await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
  expect((await staleSubmission).postDataJSON()).toMatchObject({ expected_generation: 1 });
  await expect(page.getByText("Core has a newer sandbox configuration. Refresh and review it before submitting again.")).toBeVisible();
  expect(await (await freshRead).json()).toMatchObject({ generation: 2, reset: null });
  // A new generation discards the old confirmation. The read can reconcile the
  // page, but submitting its old draft again would be an unauthorized replay.
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  expect(await writes(request)).toEqual([`POST ${resetPath}`]);

  // The administrator reviews a new confirmation before the next explicit POST.
  const reviewed = await openReset(page);
  await expect(reviewed).toContainText("Hosted Sessions will be archived permanently.");
  expect(await writes(request)).toEqual([`POST ${resetPath}`]);
  const resubmitted = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith(resetPath));
  await reviewed.getByRole("button", { name: "Reset deployment", exact: true }).click();
  expect((await resubmitted).postDataJSON()).toEqual({ expected_generation: 2, clear: "auto", deadline_seconds: 3600 });
  await expect(progress(page)).toContainText("Busy work can finish until the deadline.");
  expect(await writes(request)).toEqual([`POST ${resetPath}`, `POST ${resetPath}`]);
});

test("reset progress survives failed node and deployment reads with visible qualifications in Chinese dark mode", async ({ page, request }, info) => {
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ colorScheme: "dark" });
  await openConsole(page, request, "system?id=sandbox");
  await setDeployment(request, { resources: { allocations: 3, pending: 1 }, reset: observedReset() });
  await page.route("**/core/v1/sandbox/nodes", (route) => route.fulfill(unavailable));
  await page.reload();
  await expect(progress(page)).toContainText("offline-build-worker");
  // Node detail failure cannot block a control needing only fresh deployment state.
  await expect(progress(page).getByRole("button", { name: "Cancel reset" })).toBeEnabled();
  await page.getByRole("button", { name: "Language and appearance" }).click();
  await page.getByRole("menuitemradio", { name: "简体中文" }).click();
  const localized = page.getByRole("region", { name: "正在重置", exact: true });
  await expect(localized).toContainText("offline-build-worker");
  await expect(localized).toContainText("正在归档空闲沙箱");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await capture(page, info, "reset-progress-zh-dark-1280");
  await page.route(deploymentURL, (route) => route.fulfill(unavailable));
  await page.getByRole("button", { name: "刷新沙箱状态", exact: true }).click();
  await expect(localized).toContainText("offline-build-worker");
  await expect(localized.getByRole("button", { name: "取消重置" })).toBeDisabled();
  await expect(localized).toContainText("无法刷新重置进度。当前显示的是上次确认的数量。");
  expect(await writes(request)).toEqual([]);
});

test("a pending reset survives navigation and a lost response cannot reopen stale write controls", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  await setDeployment(request, { resources: { allocations: 1, pending: 0 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  const dialog = await openReset(page);
  let accepted!: () => void;
  const coreAccepted = new Promise<void>((resolve) => { accepted = resolve; });
  let release!: () => void;
  const responseHeld = new Promise<void>((resolve) => { release = resolve; });
  let failReads = true;
  await page.route(deploymentURL, (route) => failReads ? route.fulfill(unavailable) : route.continue());
  await page.route(resetURL, async (route) => {
    await route.fetch();
    accepted();
    await responseHeld;
    // Navigation may already have aborted the original browser request.
    await route.abort("failed").catch(() => {});
  });
  const responseLost = page.waitForEvent("requestfailed", {
    predicate: (sent) => sent.method() === "POST" && sent.url().endsWith(resetPath),
  });
  try {
    await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
    await coreAccepted;
    const cachedAt = Date.now();
    // A hash navigation keeps the application/query cache alive, unlike a reload.
    await page.evaluate(() => { window.location.hash = "overview"; });
    await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
    await openConfiguration(page);
    const resetButton = page.getByRole("button", { name: "Reset deployment", exact: true });
    await expect(resetButton).toBeDisabled();
    await expectEnrollmentBlocked(page);
    expect(Date.now() - cachedAt).toBeLessThan(30_000);
    expect(await writes(request)).toEqual([`POST ${resetPath}`]);

    release();
    await responseLost;
    // Ownership survives routing, but a departed page's local error dialog must
    // not be resurrected on the new configuration page when that request settles.
    await expect(page.getByRole("dialog", { name: "Couldn't confirm the sandbox change" })).toHaveCount(0);
    await page.getByRole("button", { name: "Overview", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
    await openConfiguration(page);
    await expect(resetButton).toBeDisabled();
    // An explicit refresh also fails; neither navigation nor failure replays POST.
    await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
    await expect(resetButton).toBeDisabled();
    expect(await writes(request)).toEqual([`POST ${resetPath}`]);

    failReads = false;
    await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
    await expect(progress(page)).toContainText("Busy work can finish until the deadline.");
    await expect(progress(page).getByRole("button", { name: "Cancel reset" })).toBeEnabled();
    expect(await writes(request)).toEqual([`POST ${resetPath}`]);
  } finally { release(); }
});

test("Nodes and configuration adopt reset and completion learned on Overview before cached node data expires", async ({ page, request }) => {
  await openConsole(page, request, "nodes");
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeEnabled();
  await openConfiguration(page);
  await expect(page.getByRole("button", { name: "Reset deployment", exact: true })).toBeEnabled();
  const cachedAt = Date.now();
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await setDeployment(request, { resources: { allocations: 3, pending: 1 }, reset: observedReset() });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Sandbox reset in progress" })).toBeVisible();
  await expectEnrollmentBlocked(page);
  await expect(progress(page)).toContainText("offline-build-worker");
  await expect(page.getByRole("button", { name: "Change resources", exact: true })).toHaveCount(0);
  expect(Date.now() - cachedAt).toBeLessThan(30_000);

  // No refresh click: shared active reset starts bounded read-only polling on configuration.
  const forced = observedReset("force");
  forced.remaining = { busy: 0, idle: 0, cleanup: 1, on_offline_nodes: 1, offline_nodes: [{ node_id: "offline-owned", name: "offline-build-worker", resources: 1 }] };
  await setDeployment(request, { resources: { allocations: 1, pending: 0 }, reset: forced });
  await expect(progress(page)).toContainText("Remaining hosted Sessions are being archived", { timeout: 12_000 });
  await expect(progress(page).getByRole("button", { name: "Force reset now" })).toHaveCount(0);

  const completedAt = Date.now();
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await setDeployment(request, { complete_reset: true });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Sandbox reset in progress" })).toHaveCount(0);
  await page.getByRole("button", { name: "Nodes", exact: true }).click();
  await expect(page.getByText("Set up sandbox hosting in System before adding nodes.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add node", exact: true })).toHaveCount(0);
  await openConfiguration(page);
  await expect(page.getByRole("heading", { name: "Where should sandboxes run?" })).toBeVisible();
  await expect(progress(page)).toHaveCount(0);
  expect(Date.now() - completedAt).toBeLessThan(30_000);
  expect(await writes(request)).toEqual([]);
});


test("signing out clears pending reset ownership and a late response cannot overwrite the new login", async ({ page, request }) => {
  await openConsole(page, request, "system?id=sandbox");
  await setDeployment(request, { resources: { allocations: 1, pending: 0 } });
  await page.getByRole("button", { name: "Refresh sandbox state", exact: true }).click();
  const dialog = await openReset(page);
  let accepted!: () => void;
  const coreAccepted = new Promise<void>((resolve) => { accepted = resolve; });
  let release!: () => void;
  const responseHeld = new Promise<void>((resolve) => { release = resolve; });
  await page.route(resetURL, async (route) => {
    const response = await route.fetch();
    accepted();
    await responseHeld;
    await route.fulfill({ response });
  });
  try {
    await dialog.getByRole("button", { name: "Reset deployment", exact: true }).click();
    await coreAccepted;
    await page.evaluate(() => { window.location.hash = "overview"; });
    await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
    await page.getByRole("button", { name: "Sign out", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Sign in to OpenAgentCore" })).toBeVisible();
    // Core completes cleanup while signed out. The new login must read generation 2.
    await setDeployment(request, { complete_reset: true });
    await page.getByLabel("Core key", { exact: true }).fill(FIXTURE_CORE_KEY);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
    await openConfiguration(page);
    await page.getByRole("button", { name: "Own machines" }).click();
    await page.getByRole("button", { name: "microsandbox Recommended" }).click();
    await page.getByRole("button", { name: /^Standard/ }).click();
    const configured = page.waitForRequest((sent) => sent.method() === "POST" && sent.url().endsWith("/sandbox/deployment"));
    await page.getByRole("button", { name: "Save configuration", exact: true }).click();
    expect((await configured).postDataJSON()).toMatchObject({ expected_generation: 2, provider: "microsandbox" });
    const add = page.getByRole("dialog", { name: "Add node" });
    await expect(add).toBeVisible();
    await add.getByRole("button", { name: "Close dialog" }).click();

    const lateResponse = page.waitForResponse((received) => received.request().method() === "POST" && received.url().endsWith(resetPath));
    release();
    await (await lateResponse).finished();
    // The old generation-1 reset response cannot restore reset progress, block
    // this login, or show the old route's result/error dialog.
    await openConfiguration(page);
    const newDialog = await openReset(page);
    await expect(newDialog).toBeVisible();
    await newDialog.getByRole("button", { name: "Back", exact: true }).click();
    await expect(progress(page)).toHaveCount(0);
    await page.getByRole("button", { name: "Nodes", exact: true }).click();
    await expect(page.getByRole("button", { name: "Add node", exact: true })).toBeEnabled();
    expect(await writes(request)).toEqual([`POST ${resetPath}`, "POST /core/v1/sandbox/deployment"]);
  } finally { release(); }
});
