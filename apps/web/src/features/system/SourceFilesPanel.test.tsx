import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { SourceFilesPanel, validHostedDestinationPath, type SourceFilesOperations } from "./SourceFilesPanel";

function operations(): SourceFilesOperations {
  return {
    uploadSourceFile: vi.fn(),
    retrieveSourceFile: vi.fn(),
    deleteSourceFile: vi.fn(),
    retrieveEnvironment: vi.fn(),
    createEnvironmentFile: vi.fn(),
    listEnvironmentFiles: vi.fn(),
  } as unknown as SourceFilesOperations;
}

describe("SourceFilesPanel", () => {
  it("accepts only canonical destination files beneath /workspace", () => {
    expect(validHostedDestinationPath("/workspace/input/notes.txt")).toBe(true);
    expect(validHostedDestinationPath("/workspace/notes.txt")).toBe(true);
    expect(validHostedDestinationPath("/workspace")).toBe(false);
    expect(validHostedDestinationPath("/workspace/")).toBe(false);
    expect(validHostedDestinationPath("/workspace/input/")).toBe(false);
    expect(validHostedDestinationPath("/workspace//notes.txt")).toBe(false);
    expect(validHostedDestinationPath("/workspace/./notes.txt")).toBe(false);
    expect(validHostedDestinationPath("/workspace/../secret")).toBe(false);
    expect(validHostedDestinationPath("/tmp/notes.txt")).toBe(false);
    expect(validHostedDestinationPath("/workspace/input\\notes.txt")).toBe(false);
  });

  it("renders Source lifecycle and discoverability boundaries without write controls", () => {
    const html = renderToStaticMarkup(
      <SourceFilesPanel operations={operations()} environmentFilesEnabled />,
    );

    expect(html).toContain("Source Files");
    expect(html).toContain("512 MiB source · 50 MiB destination");
    expect(html).toContain("Upload Source File");
    expect(html).toContain("Operate by Core ID");
    expect(html).toContain("Uploaded Source Files cannot be downloaded directly");
    expect(html).not.toContain(">Download</button>");
    expect(html).toContain("Core has no Source Files list API");
    expect(html).toContain("Copy to hosted Workspace");
    expect(html).toContain("Check Environment");
    expect(html).toContain("openai_hosted");
    expect(html).not.toContain("Copy by Source File ID");
    expect(html).not.toContain("localStorage");
    expect(html).not.toContain("file://");
  });

  it("hides Workspace copy controls when the Environment Files contract is not enabled", () => {
    const html = renderToStaticMarkup(
      <SourceFilesPanel operations={operations()} environmentFilesEnabled={false} />,
    );

    expect(html).toContain("512 MiB source");
    expect(html).not.toContain("50 MiB destination");
    expect(html).not.toContain("Copy to hosted Workspace");
    expect(html).not.toContain("Check Environment");
  });
});
