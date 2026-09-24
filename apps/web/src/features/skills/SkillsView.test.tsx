import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";

import type { Skill } from "@agents-core-web/agents-client";

import i18n from "../../i18n";
import { initialSkillsListState, SkillsListPage, type SkillsListState } from "./SkillsView";

const noop = () => undefined;

function skill(id: string, overrides: Partial<Skill> = {}): Skill {
  return {
    id,
    object: "skill",
    created_at: 1_790_208_000,
    name: "report",
    description: "Create the weekly report.",
    default_version: "1",
    latest_version: "1",
    ...overrides,
  };
}

const report = skill("skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c", { default_version: "1", latest_version: "2" });
const triage = skill("skill_9d8c7b6a-5e4f-4d3c-8b2a-1f0e9d8c7b6a", { name: "triage", description: "Sort incoming issues." });

function render(state: Partial<SkillsListState>, query = ""): string {
  return renderToStaticMarkup(
    <SkillsListPage
      state={{ ...initialSkillsListState, ...state }}
      query={query}
      onQueryChange={noop}
      onRefresh={noop}
      onLoadMore={noop}
      onOpen={noop}
      onUpload={noop}
    />,
  );
}

describe("Skills list page", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("shows loading before the first page arrives", () => {
    const html = render({ status: "loading" });
    expect(html).toContain("Loading Skills…");
    expect(html).toContain('aria-label="Refreshing…"');
  });

  it("explains Skills with a minimal SKILL.md when there are none", () => {
    const html = render({ status: "ready", skills: [] });
    expect(html).toContain("No Skills yet");
    expect(html).toContain("name: report");
    expect(html).toContain("description: Create the weekly status report.");
    expect(html.match(/Upload Skill/g)).toHaveLength(2);
    expect(html).not.toContain("disabled");
  });

  it("shows a failed first load with its reason and a retry", () => {
    const html = render({ status: "failed", error: "Agent core request failed (500)." });
    expect(html).toContain("Skills could not be loaded");
    expect(html).toContain("Agent core request failed (500).");
    expect(html).toContain("Try again");
  });

  it("explains missing Skill storage as a state, not a failure", () => {
    const html = render({ status: "storage-unavailable" });
    expect(html).toContain("Core has no Skill storage configured");
    expect(html).not.toContain("could not be loaded");
    expect(html).toMatch(/<button class="button primary" type="button" disabled="">.*Upload Skill<\/button>/);
  });

  it("lists Skills with versions, local time, a copyable ID and the not-default mark", () => {
    const html = render({ status: "ready", skills: [report, triage] });
    expect(html).toContain("report");
    expect(html).toContain("triage");
    expect(html).toContain('title="Create the weekly report."');
    expect(html).toContain("v2");
    expect(html.match(/Newer than default/g)).toHaveLength(1);
    expect(html).toContain(`title="${report.id}"`);
    expect(html).toContain('aria-label="Copy ID"');
    expect(html).toContain(new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(report.created_at * 1000)));
    expect(html).toContain("2 total");
  });

  it("offers Load more only while Core reports more pages", () => {
    expect(render({ status: "ready", skills: [report], nextAfter: report.id })).toContain("Load more");
    expect(render({ status: "ready", skills: [report], nextAfter: report.id, loadingMore: true })).toMatch(/disabled="">Loading…<\/button>/);
    expect(render({ status: "ready", skills: [report], nextAfter: null })).not.toContain("Load more");
    expect(render({ status: "ready", skills: [report], nextAfter: report.id, moreError: "timeout" })).toContain("The next page could not be loaded. timeout");
  });

  it("filters only the loaded rows and says so", () => {
    const filtered = render({ status: "ready", skills: [report, triage] }, "issues");
    expect(filtered).toContain('placeholder="Filter loaded Skills by name or description"');
    expect(filtered).toContain("triage");
    expect(filtered).not.toContain(">report<");
    expect(filtered).toContain("1 of 2");
    expect(render({ status: "ready", skills: [report, triage], nextAfter: triage.id }, "issues")).toContain("1 of 2 loaded");

    const none = render({ status: "ready", skills: [report], nextAfter: report.id }, "missing");
    expect(none).toContain("No loaded Skill matches");
    expect(none).toContain("Clear filter");
    expect(none).toContain("Load more");
  });

  it("keeps loaded rows when a refresh fails", () => {
    const html = render({ status: "ready", skills: [report], error: "Agent core request failed (502)." });
    expect(html).toContain("Refresh failed; the Skills loaded earlier are shown. Agent core request failed (502).");
    expect(html).toContain("report");
  });

  it("renders Chinese copy with API terms in English", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = render({ status: "ready", skills: [report] });
    expect(html).toContain("上传 Skill");
    expect(html).toContain('placeholder="仅筛选已加载项：按名称或描述"');
    expect(html).toContain("有未启用的新版本");
    expect(render({ status: "storage-unavailable" })).toContain("Core 未配置 Skill 存储");
  });
});
