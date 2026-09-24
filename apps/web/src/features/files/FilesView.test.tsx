import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";

import type { SourceFile, SourceFileListEntry } from "@agents-core-web/agents-client";

import i18n from "../../i18n";
import { FilesListPage, initialFilesListState, type FilesListPageProps, type FilesListState } from "./FilesView";

const noop = () => undefined;

function file(id: string, filename: string, bytes: number): SourceFile {
  return {
    id, object: "file", bytes, created_at: 1_790_208_000, filename,
    purpose: "user_data", status: "processed", expires_at: null, status_details: null,
  };
}

const notes = file("file-16e1f26e-8cf6-4272-9c31-d470b08d31af", "notes.txt", 3);
const input = file("file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "input.csv", 42 * 1024);
const unknown: SourceFileListEntry = { id: "file-3c8d0e21-5f60-4b7c-9d8e-0f1a2b3c4d5e", object: "file", unrecognized: true };

function render(state: Partial<FilesListState>, props: Partial<FilesListPageProps> = {}): string {
  return renderToStaticMarkup(
    <FilesListPage
      state={{ ...initialFilesListState, ...state }}
      order="desc"
      query=""
      upload={{ kind: "idle" }}
      notice={null}
      highlightId={null}
      confirmId={null}
      deletingId={null}
      onOrderChange={noop}
      onQueryChange={noop}
      onRefresh={noop}
      onLoadMore={noop}
      onUpload={noop}
      onDismissUpload={noop}
      onConfirm={noop}
      onDelete={noop}
      {...props}
    />,
  );
}

describe("Files page", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("shows loading before the first page", () => {
    const html = render({ status: "loading" });
    expect(html).toContain("Loading files…");
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>.*Upload file/u);
  });

  it("explains that user data files cannot be downloaded and offers no download action", () => {
    const html = render({ status: "ready", files: [notes, input] });
    expect(html).toContain("User data files cannot be downloaded");
    expect(html).not.toMatch(/>\s*Download\s*</u);
    expect(html.toLowerCase()).not.toContain("<a ");
  });

  it("lists name, size, creation time, purpose and a copyable File ID", () => {
    const html = render({ status: "ready", files: [notes, input] });
    for (const value of ["notes.txt", "input.csv", "3 B", "42.0 KiB", "user_data", "Copy File ID", "Delete notes.txt"]) {
      expect(html).toContain(value);
    }
    expect(html).toContain(`title="${notes.id}"`);
    expect(html).toContain("2 loaded");
  });

  it("says the filter covers loaded rows only", () => {
    const html = render({ status: "ready", files: [notes, input] }, { query: "csv" });
    expect(html).toContain('placeholder="Filter loaded files by name or ID"');
    expect(html).toContain("1 of 2 loaded");
    expect(html).not.toContain("notes.txt");
  });

  it("explains an empty filter result and keeps Load more", () => {
    const html = render({ status: "ready", files: [notes], nextAfter: notes.id }, { query: "missing" });
    expect(html).toContain("No loaded files match");
    expect(html).toContain("Load more");
  });

  it("offers ordering and cursor pagination", () => {
    const html = render({ status: "ready", files: [notes], nextAfter: notes.id });
    expect(html).toContain("Newest first");
    expect(html).toContain("Oldest first");
    expect(html).toContain("Load more");
    expect(render({ status: "ready", files: [notes], nextAfter: null })).not.toContain("Load more");
  });

  it("keeps an unrecognized row with only its ID", () => {
    const html = render({ status: "ready", files: [notes, unknown] });
    expect(html).toContain("Unrecognized file");
    expect(html).toContain("only the File ID is shown");
    expect(html).toContain(`title="${unknown.id}"`);
  });

  it("states the three deletion consequences in the confirmation", () => {
    const html = render({ status: "ready", files: [notes] }, { confirmId: notes.id });
    expect(html).toContain("Delete notes.txt?");
    expect(html).toContain("Copies already placed in Session workspaces are not affected");
    expect(html).toContain("neither are existing Sessions");
    expect(html).toContain("Environment Template that references this File ID will fail");
    expect(html).toContain("Delete file");
  });

  it("highlights a newly uploaded row", () => {
    expect(render({ status: "ready", files: [notes] }, { highlightId: notes.id })).toContain('class="files-row-new"');
  });

  it("asks for a refresh instead of retrying an uncertain upload", () => {
    const html = render({ status: "ready", files: [notes] }, { upload: { kind: "uncertain", name: "input.csv" } });
    expect(html).toContain("may have reached Core. Refresh the list to check before uploading it again.");
    expect(html).not.toContain("Retry upload");
  });

  it("distinguishes empty, unsupported, storage and failed states", () => {
    expect(render({ status: "ready", files: [] })).toContain("No files yet");
    expect(render({ status: "unsupported", unsupportedStatus: 405 })).toContain("HTTP 405");
    expect(render({ status: "storage-unavailable" })).toContain("File storage is not configured");
    const failed = render({ status: "failed", error: "Core rejected the connection credentials." });
    expect(failed).toContain("Files could not be loaded");
    expect(failed).toContain("Try again");
  });

  it("keeps rows visible when a refresh fails", () => {
    const html = render({ status: "ready", files: [notes], error: "The request did not reach Core or its response was lost." });
    expect(html).toContain("The list could not be refreshed");
    expect(html).toContain("notes.txt");
  });

  it("renders Chinese copy with API terms kept in English", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = render({ status: "ready", files: [notes] }, { confirmId: notes.id });
    expect(html).toContain("文件");
    expect(html).toContain("复制 File ID");
    expect(html).toContain("按名称或 ID 筛选已加载的文件");
    expect(html).toContain("已复制到 Session 工作区的副本不受影响");
    expect(html).toContain("不能下载");
  });
});
