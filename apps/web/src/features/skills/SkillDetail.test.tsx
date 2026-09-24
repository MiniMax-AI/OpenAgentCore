import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import type { Skill, SkillVersion, SkillVersionList } from "@agents-core-web/agents-client";

import { SkillDetailPage, type SkillDetailPageProps, type SkillVersionsState } from "./SkillDetail";
import { setSkillDefaultVersion } from "./skill-operations";
import { SkillUploadDialog, SkillBundlePreviewView } from "./SkillUploadDialog";

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
      onUploadVersion={noop}
      onDownload={noop}
      onDeleteSkill={noop}
      onSetDefault={noop}
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
    for (const action of ["Upload new version", "Download", "Delete Skill"]) expect(html).toContain(action);
    expect(html).toContain('title="Download default version"');
    expect(html).toContain("Default version");
    expect(html).toContain("Newer than default");
    expect(html.match(/status-dot-ok/g)).toHaveLength(1);
    expect(html).toContain("Make v3 the default");
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

  it("refreshes the name after the default version changes", async () => {
    const before = render({});
    expect(before).toContain(">report</h1>");

    const renamed = { ...skill, name: "report-v3", description: "Create the quarterly report.", default_version: "3" };
    const core = {
      updateSkillDefaultVersion: vi.fn(async () => renamed),
      listSkillVersions: vi.fn(async (): Promise<SkillVersionList> => ({
        object: "list",
        data: [version("3", { name: "report-v3" }), version("2"), version("1")],
        has_more: false,
        first_id: "skillver_3",
        last_id: "skillver_1",
      })),
    };
    const result = await setSkillDefaultVersion(core, skillId, "3");
    const after = render({ skill: result.skill, versions: versions(result.versions, result.nextAfter) });
    expect(after).toContain(">report-v3</h1>");
    expect(after).toContain("Create the quarterly report.");
    expect(after).not.toContain("Newer than default");
    expect(after).toContain("Make v2 the default");
    expect(after).not.toContain("Make v3 the default");
  });

  it("shows loading, missing and failed states", () => {
    expect(render({ skill: null, status: "loading" })).toContain("Loading Skill…");
    const missing = render({ status: "missing" });
    expect(missing).toContain("This Skill no longer exists");
    expect(missing).toMatch(/<button class="button primary" type="button" disabled="">/);
    expect(render({ skill: null, status: "failed", error: "boom" })).toContain("The Skill could not be loaded");
    expect(render({ versions: { ...versions([]), status: "failed", error: "timeout" } })).toContain("Versions could not be loaded");
    expect(render({ notice: "The default version cannot be deleted. Refresh and try again." })).toContain('role="alert">The default version cannot be deleted. Refresh and try again.');
  });

  it("paginates versions with Load more", () => {
    expect(render({ versions: versions([version("3")], "skillver_3") })).toContain("Load more");
    expect(render({})).not.toContain("Load more");
  });
});

describe("Skill upload dialog", () => {
  const core = { uploadSkill: vi.fn(), uploadSkillVersion: vi.fn() };

  it("offers ZIP and folder uploads with the format requirements", () => {
    const html = renderToStaticMarkup(<SkillUploadDialog open core={core} target={{ kind: "skill" }} onClose={noop} onUploaded={noop} />);
    expect(html).toContain("Upload Skill");
    expect(html).toContain('role="radiogroup" aria-label="Upload from"');
    expect(html).toContain('accept=".zip,application/zip"');
    expect(html).toContain("Format requirements");
    expect(html).toContain("Hooks, permission controls and subagent directives are rejected.");
    expect(html).not.toContain('type="checkbox"');
    expect(html).toMatch(/<button class="button primary" type="button" disabled="">/);
  });

  it("offers an unchecked default choice for a new version", () => {
    const html = renderToStaticMarkup(<SkillUploadDialog open core={core} target={{ kind: "version", skill }} onClose={noop} onUploaded={noop} />);
    expect(html).toContain("Upload a new version of report");
    expect(html).toContain('<input type="checkbox"/>');
    expect(html).toContain("Make this the default version");
  });

  it("renders frontmatter values as plain text", () => {
    const html = renderToStaticMarkup(<SkillBundlePreviewView preview={{
      kind: "directory",
      topLevel: "report",
      fileCount: 2,
      totalBytes: 2048,
      archiveBytes: null,
      name: "report",
      description: "<img src=x onerror=alert(1)> **bold**",
      errors: [{ code: "missing-manifest", folder: "report" }],
      warnings: [{ code: "hidden-files", paths: ["report/.DS_Store"], more: 0 }],
    }} />);
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt; **bold**");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<strong>bold");
    expect(html).toContain("2.0 KiB");
    expect(html).toContain("report/SKILL.md is missing.");
    expect(html).toContain("Hidden files are uploaded as they are: report/.DS_Store");
  });
});
