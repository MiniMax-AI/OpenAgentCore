import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";

import i18n from "../../i18n";
import {
  type AgentUsageModel,
  AgentUsagePanel,
  AgentUsageRangeControl,
  AgentUsageStatus,
  formatUsageCoverage,
  OtherAgentUsageSection,
} from "./AgentUsage";
import { aggregateAgentUsage, type AgentUsageTotals, type UsageSessionRecord } from "./agent-usage";
import { type AgentUsageController, initialAgentUsageState } from "./use-agent-usage";

const now = Math.floor(Date.now() / 1_000);

function controller(overrides: Partial<AgentUsageController> = {}): AgentUsageController {
  return {
    ...initialAgentUsageState,
    status: "ready",
    setRange: () => undefined,
    cancel: () => undefined,
    resume: () => undefined,
    reload: () => undefined,
    ...overrides,
  };
}

function record(id: string, agentId: string, overrides: Partial<UsageSessionRecord> = {}): UsageSessionRecord {
  return { id, agentId, status: "idle", createdAt: now - 3_600, lastActiveAt: now - 60, usage: null, ...overrides };
}

const records: UsageSessionRecord[] = [
  record("s1", "agent_a", { status: "in_progress", usage: { input: 1_000, output: 250, total: 1_250, cached: 400, reasoning: 90 } }),
  record("s2", "agent_a", { status: "failed", lastActiveAt: now - 7_200 }),
  record("s3", "agent_a", { usage: { input: 10, output: 5, total: 15, cached: 0, reasoning: 0 } }),
  record("s4", "agent_quiet"),
  record("s5", "agent_deleted", { usage: { input: 60, output: 40, total: 100, cached: 0, reasoning: 0 } }),
  record("s6", "agent_inline_9"),
];

function model(overrides: Partial<AgentUsageController> = {}): AgentUsageModel {
  const usage = controller(overrides);
  return {
    controller: usage,
    report: usage.status === "ready" ? aggregateAgentUsage(records, ["agent_a", "agent_quiet", "agent_new"], null) : null,
  };
}

function totals(sessions: number, reported: number): AgentUsageTotals {
  return {
    sessions,
    reported,
    statuses: { idle: sessions, in_progress: 0, requires_action: 0, failed: 0, unknown: 0 },
    tokens: { input: null, output: null, total: null, cached: null, reasoning: null },
    lastActiveAt: null,
  };
}

describe("Agent usage presentation", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("never rounds coverage to a misleading 0% or 100%", () => {
    expect(formatUsageCoverage(totals(0, 0), "No data", "en")).toBe("—");
    expect(formatUsageCoverage(totals(3, 0), "No data", "en")).toBe("No data");
    expect(formatUsageCoverage(totals(3, 1), "No data", "en")).toBe("33%");
    expect(formatUsageCoverage(totals(2_000, 1), "No data", "en")).toBe("0.1%");
    expect(formatUsageCoverage(totals(1_000, 999), "No data", "en")).toBe("99%");
    expect(formatUsageCoverage(totals(5, 5), "No data", "en")).toBe("100%");
  });

  it("states the range basis and the fixed caveat", () => {
    const html = renderToStaticMarkup(<AgentUsageRangeControl usage={model()} />);
    expect(html).toContain("Sessions created");
    expect(html).toContain('role="radiogroup" aria-label="Sessions created"');
    for (const label of ["All time", "Last 7 days", "Last 30 days"]) expect(html).toContain(label);
    expect(html).toContain('aria-checked="true" tabindex="0" class="active">All time');
    expect(html).toContain("it is not split by day");
    expect(html).toContain("Data comes from cumulative Session usage reported by Core. It excludes unreported usage and is not a basis for billing.");
  });

  it("shows read progress with cancellation, the large-collection hint, retry and continue", () => {
    const loading = renderToStaticMarkup(<AgentUsageStatus usage={model({ status: "loading", range: "30d", progress: { pages: 124, sessions: 12_345 } })} />);
    expect(loading).toContain('<span role="status">Reading Sessions… 12,345 read</span>');
    expect(loading).toContain(">Cancel</button>");
    expect(loading).toContain("More than 10,000 Sessions: statistics may be slow. Try a shorter time range first.");

    const shortest = renderToStaticMarkup(<AgentUsageStatus usage={model({ status: "loading", range: "7d", progress: { pages: 120, sessions: 12_000 } })} />);
    expect(shortest).toContain("More than 10,000 Sessions: statistics may be slow.");
    expect(shortest).not.toContain("Try a shorter time range");

    const small = renderToStaticMarkup(<AgentUsageStatus usage={model({ status: "loading", progress: { pages: 1, sessions: 100 } })} />);
    expect(small).not.toContain("statistics may be slow");

    const failed = renderToStaticMarkup(<AgentUsageStatus usage={model({ status: "failed", error: "Fixture Session list failed." })} />);
    expect(failed).toContain('<span role="alert">Couldn’t read Sessions. Fixture Session list failed.</span>');
    expect(failed).toContain(">Retry</button>");

    const cancelled = renderToStaticMarkup(<AgentUsageStatus usage={model({ status: "cancelled", progress: { pages: 3, sessions: 300 } })} />);
    expect(cancelled).toContain("Stopped after reading 300 Sessions.");
    expect(cancelled).toContain(">Continue</button>");

    expect(renderToStaticMarkup(<AgentUsageStatus usage={model()} />)).toBe("");
  });

  it("renders the complete detail breakdown for one Agent", () => {
    const html = renderToStaticMarkup(<AgentUsagePanel usage={model()} agentId="agent_a" />);
    expect(html).toContain(">Usage</h2>");
    expect(html).toMatch(/<span>Sessions<\/span><\/dt><dd><span class="kpi-value">3<\/span>/);
    expect(html).toContain('<span class="kpi-value">1,265</span>');
    expect(html).toContain('<span class="kpi-value">67%</span>');
    expect(html).toContain("In progress <strong>1</strong>");
    expect(html).toContain("Requires action <strong>0</strong>");
    expect(html).toContain("Failed <strong>1</strong>");
    expect(html).toContain("Idle <strong>1</strong>");
    expect(html).not.toContain("Unrecognized");
    for (const [label, value] of [["Input", "1,010"], ["Output", "255"], ["Total", "1,265"], ["Cached input", "400"], ["Reasoning", "90"]]) {
      expect(html).toContain(`<dt>${label}</dt><dd>${value}</dd>`);
    }
    expect(html).toContain("2 of 3 Sessions reported usage");
    expect(html).toContain("1 minute ago");
    expect(html).toContain("not a basis for billing");
  });

  it("shows missing usage as no data and an unused Agent without figures", () => {
    const quiet = renderToStaticMarkup(<AgentUsagePanel usage={model()} agentId="agent_quiet" />);
    expect(quiet).toContain('<span class="kpi-value">No data</span>');
    expect(quiet).toContain("<dt>Total</dt><dd>No data</dd>");
    expect(quiet).toContain("0 of 1 Session reported usage");
    expect(quiet).not.toContain(">0%<");

    const unused = renderToStaticMarkup(<AgentUsagePanel usage={model()} agentId="agent_new" />);
    expect(unused).toMatch(/<span>Sessions<\/span><\/dt><dd><span class="kpi-value">0<\/span>/);
    expect(unused).toContain("<dt>Total</dt><dd>—</dd>");

    const pending = renderToStaticMarkup(<AgentUsagePanel usage={model({ status: "loading" })} agentId="agent_a" />);
    expect(pending).toContain('aria-busy="true"');
    expect(pending).toContain('<span class="kpi-value">…</span>');
    expect(pending).not.toContain("Cached input");
  });

  it("keeps Sessions without a saved Agent visible under their raw Agent ID", () => {
    const html = renderToStaticMarkup(<OtherAgentUsageSection usage={model()} />);
    expect(html).toContain(">Other Agents</h2>");
    expect(html).toContain("<code>agent_deleted</code>");
    expect(html).toContain("<code>agent_inline_9</code>");
    expect(html).toContain('<td class="numeric">100</td><td class="numeric">100%</td>');
    expect(html).toContain('<td class="numeric">No data</td>');
    expect(html).not.toContain("agent_quiet");

    const filtered = renderToStaticMarkup(<OtherAgentUsageSection usage={model()} query="inline" />);
    expect(filtered).toContain("agent_inline_9");
    expect(filtered).not.toContain("agent_deleted");

    const loading = renderToStaticMarkup(<OtherAgentUsageSection usage={model({ status: "loading" })} />);
    expect(loading).toBe("");
  });

  it("uses Chinese copy with API terms kept in English", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = renderToStaticMarkup(<><AgentUsagePanel usage={model()} agentId="agent_quiet" /><OtherAgentUsageSection usage={model()} /></>);
    expect(html).toContain("Session 创建时间");
    expect(html).toContain("无数据");
    expect(html).toContain("其他 Agent");
    expect(html).toContain("数据来自 Core 报告的 Session 累计用量，不包含未报告的部分，不能作为计费依据。");
  });
});
