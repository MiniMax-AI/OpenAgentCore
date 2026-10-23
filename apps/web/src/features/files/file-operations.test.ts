import { describe, expect, it } from "vitest";

import type { SourceFile, SourceFileListEntry } from "@oac/agents-client";

import { filterFiles } from "./file-operations";

function file(id: string, filename: string, created = 1_790_208_000): SourceFile {
  return {
    id, object: "file", bytes: 3, created_at: created, filename,
    purpose: "user_data", status: "processed", expires_at: null, status_details: null,
  };
}

const notes = file("file-16e1f26e-8cf6-4272-9c31-d470b08d31af", "notes.txt");
const input = file("file-2b7c9d10-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "Input.CSV");
const unknown: SourceFileListEntry = { id: "file-3c8d0e21-5f60-4b7c-9d8e-0f1a2b3c4d5e", object: "file", unrecognized: true };

describe("loaded-row filter", () => {
  it("matches file names and IDs case-insensitively", () => {
    expect(filterFiles([notes, input, unknown], "csv")).toEqual([input]);
    expect(filterFiles([notes, input, unknown], "3C8D0E21")).toEqual([unknown]);
    expect(filterFiles([notes, input, unknown], "  ")).toEqual([notes, input, unknown]);
  });
});
