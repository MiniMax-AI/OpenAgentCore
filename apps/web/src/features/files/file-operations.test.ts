import { describe, expect, it, vi } from "vitest";

import type { SourceFile, SourceFileListEntry } from "@oac/agents-client";

import {
  filterFiles,
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
