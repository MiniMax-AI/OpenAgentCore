import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";

// Per-Agent usage on Resources › Agents: sums of reported Session usage,
// coverage that never turns missing usage into zero, an "Other Agents" group
// for deleted and inline Agents, and time ranges by Session creation time.

const fixtureBaseUrl = `http://127.0.0.1:${process.env.AGENTS_FIXTURE_PORT ?? 18092}`;
/** Fixture Session timestamps are anchored to this instant. */
const fixtureBaseline = 1_789_438_800;

async function resetFixture(request: APIRequestContext) {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/reset`)).ok()).toBe(true);
}

async function controlFixture(request: APIRequestContext, values: Record<string, unknown>) {
  expect((await request.post(`${fixtureBaseUrl}/__fixture/control`, { data: values })).ok()).toBe(true);
}

function collectPageErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  return errors;
}

function agentRow(page: Page, name: string): Locator {
  return page.getByRole("table", { name: "Agents", exact: true }).getByRole("row")
    .filter({ has: page.getByRole("button", { name: new RegExp(`^Edit ${name} \\(`) }) });
}

/** Sessions, tokens and coverage of one saved Agent row. */
async function expectAgentUsage(page: Page, name: string, [sessions, tokens, coverage]: [string, string, string]) {
  // Cells: model, harness, tools, Sessions, tokens, coverage, last active, updated, actions.
  const cells = agentRow(page, name).getByRole("cell");
  await expect(cells.nth(3)).toHaveText(sessions);
  await expect(cells.nth(4)).toHaveText(tokens);
  await expect(cells.nth(5)).toHaveText(coverage);
}

function otherRow(page: Page, agentId: string): Locator {
  return page.getByRole("region", { name: "Other Agents", exact: true }).getByRole("row").filter({ hasText: agentId });
}

async function expectOtherUsage(page: Page, agentId: string, [sessions, tokens, coverage]: [string, string, string]) {
  const cells = otherRow(page, agentId).getByRole("cell");
  await expect(cells.nth(0)).toHaveText(sessions);
  await expect(cells.nth(1)).toHaveText(tokens);
  await expect(cells.nth(2)).toHaveText(coverage);
}

test.beforeEach(async ({ request }) => {
  await resetFixture(request);
});

test("sums reported usage per Agent, keeps missing usage out of totals and follows the creation-time range", async ({ page, request }) => {
  const errors = collectPageErrors(page);
  // Small pages make the usage reader follow Core's cursor across several requests.
  await controlFixture(request, { usageScenario: 1, sessionListPageSize: 3 });
  await page.clock.setFixedTime(new Date((fixtureBaseline + 120) * 1_000));
  await page.goto("/#agents");

  await expect(page.locator("main").getByRole("heading", { level: 1, name: "Agents", exact: true })).toBeVisible();
  await expect(page.getByText(
    "Data comes from cumulative Session usage reported by Core. It excludes unreported usage and is not a basis for billing.",
    { exact: true },
  )).toBeVisible();
  const ranges = page.getByRole("radiogroup", { name: "Sessions created", exact: true });
  await expect(ranges.getByRole("radio")).toHaveText(["All time", "Last 7 days", "Last 30 days"]);
  await expect(ranges.getByRole("radio", { name: "All time" })).toHaveAttribute("aria-checked", "true");

  // All time: Lifecycle Agent has four Sessions, two with reported usage (140 + 10).
  await expectAgentUsage(page, "Lifecycle Agent", ["4", "150", "50%"]);
  await expectAgentUsage(page, "Second Agent", ["1", "60", "100%"]);
  await expectAgentUsage(page, "Saved-only Tool Agent", ["0", "—", "—"]);
  // Sessions of a deleted Agent and an inline Agent stay visible under their raw IDs.
  await expectOtherUsage(page, "agent_deleted", ["2", "1,000", "50%"]);
  await expectOtherUsage(page, "agent_inline_usage", ["1", "No data", "No data"]);

  await ranges.getByRole("radio", { name: "Last 7 days" }).click();
  await expectAgentUsage(page, "Lifecycle Agent", ["3", "140", "33%"]);
  await expectAgentUsage(page, "Second Agent", ["0", "—", "—"]);
  await expectOtherUsage(page, "agent_deleted", ["1", "1,000", "100%"]);
  await expect(otherRow(page, "agent_inline_usage")).toHaveCount(0);

  await ranges.getByRole("radio", { name: "Last 30 days" }).click();
  await expectAgentUsage(page, "Lifecycle Agent", ["3", "140", "33%"]);
  await expectAgentUsage(page, "Second Agent", ["1", "60", "100%"]);
  await expectOtherUsage(page, "agent_deleted", ["1", "1,000", "100%"]);

  await ranges.getByRole("radio", { name: "All time" }).click();
  await expectAgentUsage(page, "Lifecycle Agent", ["4", "150", "50%"]);

  // The Agent detail view carries the complete breakdown for the same range.
  await page.getByRole("button", { name: /^Edit Lifecycle Agent/ }).click();
  const panel = page.getByRole("region", { name: "Usage", exact: true });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("radio", { name: "All time" })).toHaveAttribute("aria-checked", "true");
  await expect(panel).toContainText("In progress 0");
  await expect(panel).toContainText("Requires action 0");
  await expect(panel).toContainText("Failed 1");
  await expect(panel).toContainText("Idle 3");
  const tokens = panel.locator(".agent-usage-tokens");
  await expect(tokens.locator("dt")).toHaveText(["Input", "Output", "Total", "Cached input", "Reasoning"]);
  await expect(tokens.locator("dd")).toHaveText(["107", "43", "150", "20", "10"]);
  await expect(panel).toContainText("2 of 4 Sessions reported usage");
  await expect(panel).toContainText("not a basis for billing");

  await panel.getByRole("radio", { name: "Last 7 days" }).click();
  await expect(panel).toContainText("1 of 3 Sessions reported usage");
  await expect(tokens.locator("dd")).toHaveText(["100", "40", "140", "20", "10"]);

  expect(errors).toEqual([]);
});

test("shows a failed usage read with retry and keeps the Agent list usable", async ({ page, request }) => {
  const errors = collectPageErrors(page);
  await controlFixture(request, { usageScenario: 1 });
  await page.clock.setFixedTime(new Date((fixtureBaseline + 120) * 1_000));
  await page.goto("/#agents");
  await expectAgentUsage(page, "Lifecycle Agent", ["4", "150", "50%"]);

  // The next Session list read fails once; reloading usage reports it and a retry recovers.
  await controlFixture(request, { sessionListStatus: 500 });
  await page.getByRole("button", { name: "Refresh Agents", exact: true }).click();
  const failure = page.getByRole("alert").filter({ hasText: "Couldn’t read Sessions." });
  await expect(failure).toBeVisible();
  await expect(agentRow(page, "Lifecycle Agent").getByRole("cell").nth(3)).toHaveText("—");
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(failure).toHaveCount(0);
  await expectAgentUsage(page, "Lifecycle Agent", ["4", "150", "50%"]);

  expect(errors).toEqual([]);
});
