import { describe, expect, it, vi } from "vitest";

import { AgentCoreError, type SourceFile, type SourceFileListEntry } from "@oac/agents-client";

import i18n from "../../i18n";
import {
  classifyFilesListError,
  filesErrorReason,
  filterFiles,
  isDefiniteRejection,
  maxFileUploadBytes,
  readFilesPage,
  uploadPrecheck,
} from "./file-operations";

function file(id: string, filename: string, created = 1_790_208_000): SourceFile {
  return {
    id, object: "file", bytes: 3, created_at: created, filename,
    purpose: "user_data", status: "processed", expires_at: null, status_details: null,
  };
}

const notes = file("file-16e1f26e-8cf6-4272-9c31-d470b08d31af", "notes.txt");
const input = file("file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "Input.CSV");
const unknown: SourceFileListEntry = { id: "file-3c8d0e21-5f60-4b7c-9d8e-0f1a2b3c4d5e", object: "file", unrecognized: true };
const t = (key: string) => i18n.t(key as never, { ns: "files" }) as string;

describe("upload precheck", () => {
  it("accepts an empty file and the 512 MiB bound", () => {
    expect(uploadPrecheck({ name: "empty.bin", size: 0 })).toBeNull();
    expect(uploadPrecheck({ name: "big.bin", size: maxFileUploadBytes })).toBeNull();
  });

  it("rejects oversized files before sending", () => {
    expect(uploadPrecheck({ name: "big.bin", size: maxFileUploadBytes + 1 })).toBe("too-large");
  });

  it("bounds the name to 1–1024 UTF-8 bytes without NUL", () => {
    expect(uploadPrecheck({ name: "", size: 1 })).toBe("bad-name");
    expect(uploadPrecheck({ name: "a\0b", size: 1 })).toBe("bad-name");
    expect(uploadPrecheck({ name: "é".repeat(512), size: 1 })).toBeNull();
    expect(uploadPrecheck({ name: "é".repeat(513), size: 1 })).toBe("bad-name");
  });
});

describe("error mapping", () => {
  it("treats only 4xx answers as definite rejections", () => {
    expect(isDefiniteRejection(new AgentCoreError("bad", 400))).toBe(true);
    expect(isDefiniteRejection(new AgentCoreError("gone", 404))).toBe(true);
    // A lost response, a server error or an unrecognized success body may have stored the file.
    expect(isDefiniteRejection(new TypeError("Failed to fetch"))).toBe(false);
    expect(isDefiniteRejection(new AgentCoreError("down", 503))).toBe(false);
    expect(isDefiniteRejection(new AgentCoreError("bad body", 502, "invalid_source_file"))).toBe(false);
  });

  it("classifies a failed first list read", () => {
    expect(classifyFilesListError(new AgentCoreError("missing", 404))).toBe("unsupported");
    expect(classifyFilesListError(new AgentCoreError("method", 405))).toBe("unsupported");
    expect(classifyFilesListError(new AgentCoreError("busy", 503))).toBe("failed");
    expect(classifyFilesListError(new TypeError("Failed to fetch"))).toBe("failed");
  });

  it("gives a short reason without transport details", () => {
    expect(filesErrorReason(new AgentCoreError("Invalid purpose.", 400, null, "purpose"), t)).toBe("Invalid purpose.");
    expect(filesErrorReason(new AgentCoreError("no", 401), t)).toBe("Core rejected the connection credentials.");
    expect(filesErrorReason(new AgentCoreError("big", 413, "request_too_large"), t)).toBe("The file is larger than Core accepts.");
    expect(filesErrorReason(new AgentCoreError("x", 502, "invalid_source_file_list"), t)).toBe("Core returned a response this console does not recognize.");
    expect(filesErrorReason(new TypeError("Failed to fetch http://10.0.0.1"), t)).toBe("The request did not reach Core or its response was lost.");
  });
});

describe("loaded-row filter", () => {
  it("matches file names and IDs case-insensitively", () => {
    expect(filterFiles([notes, input, unknown], "csv")).toEqual([input]);
    expect(filterFiles([notes, input, unknown], "3C8D0E21")).toEqual([unknown]);
    expect(filterFiles([notes, input, unknown], "  ")).toEqual([notes, input, unknown]);
  });
});

describe("pagination", () => {
  it("reads 100 at a time with the chosen order and appends the next page", async () => {
    const listSourceFiles = vi.fn()
      .mockResolvedValueOnce({ object: "list", data: [notes], has_more: true, first_id: notes.id, last_id: notes.id })
      .mockResolvedValueOnce({ object: "list", data: [input], has_more: false, first_id: input.id, last_id: input.id });

    const first = await readFilesPage({ listSourceFiles }, [], "asc", undefined);
    const second = await readFilesPage({ listSourceFiles }, first.values, "asc", first.nextAfter ?? undefined);

    expect(listSourceFiles.mock.calls.map(([options]) => options)).toEqual([
      { limit: 100, order: "asc", after: undefined, signal: undefined },
      { limit: 100, order: "asc", after: notes.id, signal: undefined },
    ]);
    expect(first.nextAfter).toBe(notes.id);
    expect(second).toEqual({ values: [notes, input], nextAfter: null });
  });

  it("rejects a page that repeats a loaded File", async () => {
    const listSourceFiles = vi.fn().mockResolvedValue({ object: "list", data: [notes], has_more: false, first_id: notes.id, last_id: notes.id });
    await expect(readFilesPage({ listSourceFiles }, [notes], "desc", notes.id)).rejects.toThrow("duplicate");
  });
});
