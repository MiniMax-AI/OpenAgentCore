import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import type { Skill, SkillVersion, SkillVersionList } from "@agents-core-web/agents-client";

import { SkillDetailPage, type SkillDetailPageProps, type SkillVersionsState } from "./SkillDetail";

const noop = () => undefined;
const skillId = "skill_3f1c2a9e-7b4d-4e8a-9c21-5d6e7f8a9b0c";

const skill: Skill = {
  id: skillId,
  object: "skill",
  created_at: 1_790_208_000,
  name: "report",
  description: "Create the report.",
  default_version: "2",
  latest_version: "3",
};

function version(number: string, overrides: Partial<SkillVersion> = {}): SkillVersion {
  return {
    id: `skillver_${number}`,
    object: "skill.version",
    skill_id: skillId,
    version: number,
    name: "report",
    description: `Version ${number}.`,
    created_at: 1_790_208_000 + Number(number),
    ...overrides,
  };
}

function versions(items: SkillVersion[], nextAfter: string | null = null): SkillVersionsState {
  return { status: "ready", items, nextAfter, error: null, loadingMore: false, moreError: null };
}

function render(props: Partial<SkillDetailPageProps>): string {
  return renderToStaticMarkup(
    <SkillDetailPage
      skill={skill}
      status="ready"
      error={null}
      versions={versions([version("3"), version("2"), version("1")])}
      refreshing={false}
      notice={null}
      downloading={null}
      onBack={noop}
      onRefresh={noop}
      onDownload={noop}
      onDeleteSkill={noop}
      onDeleteVersion={noop}
      onLoadMoreVersions={noop}
      {...props}
    />,
  );
}

/** The delete control of one version row, found by its accessible name. */
function deleteControl(html: string, number: string): string {
  const match = new RegExp(`(<span class="skill-disabled-action"[^>]*>)?<button[^>]*aria-label="Delete v${number}[^"]*"[^>]*>`).exec(html);
  if (!match) throw new Error(`No delete control for v${number}`);
  return match[0];
}

describe("Skill detail page", () => {
  it("shows the Skill facts, header actions and version marks", () => {
    const html = render({});
    expect(html).toContain("report");
    expect(html).toContain("Create the report.");
    expect(html).toContain(skillId);
    expect(html).toContain('aria-label="Back to Skills"');
    for (const action of ["Download", "Delete Skill"]) expect(html).toContain(action);
    expect(html).not.toContain("Upload new version");
    expect(html).not.toContain("Set as default");
    expect(html).toContain('title="Download default version"');
    expect(html).toContain("Default version");
    expect(html).toContain("Newer than default");
    expect(html.match(/status-dot-ok/g)).toHaveLength(1);
    expect(html).not.toContain("Make v3 the default");
    expect(html).not.toContain("Make v2 the default");
    expect(html).toContain('aria-label="Download v1"');
  });

  it("offers the three version deletion states", () => {
    const three = render({});
    // A non-default version can be deleted.
    expect(deleteControl(three, "3")).not.toContain("disabled");
    expect(deleteControl(three, "1")).not.toContain("disabled");
    // The default cannot be deleted while other versions remain; hovering explains why.
    const blocked = deleteControl(three, "2");
    expect(blocked).toContain('title="Make another version the default first"');
    expect(blocked).toContain("disabled");

    // Until every page is loaded, other versions may remain.
    const partial = render({ skill: { ...skill, default_version: "3" }, versions: versions([version("3")], "skillver_3") });
    expect(deleteControl(partial, "3")).toContain("disabled");

    // The sole version can be deleted, which deletes the Skill; the dialog says so.
    const only = render({ skill: { ...skill, default_version: "1", latest_version: "1" }, versions: versions([version("1")]) });
    expect(deleteControl(only, "1")).not.toContain("disabled");
  });



  it("shows loading, missing and failed states", () => {
    expect(render({ skill: null, status: "loading" })).toContain("Loading Skill…");
    const missing = render({ status: "missing" });
    expect(missing).toContain("This Skill no longer exists");
    expect(missing).toMatch(/<button class="button danger" type="button" disabled="">/);
    expect(render({ skill: null, status: "failed", error: "boom" })).toContain("The Skill could not be loaded");
    expect(render({ versions: { ...versions([]), status: "failed", error: "timeout" } })).toContain("Versions could not be loaded");
    expect(render({ notice: "The default version cannot be deleted. Refresh and try again." })).toContain('role="alert">The default version cannot be deleted. Refresh and try again.');
  });

  it("paginates versions with Load more", () => {
    expect(render({ versions: versions([version("3")], "skillver_3") })).toContain("Load more");
    expect(render({})).not.toContain("Load more");
  });
});

