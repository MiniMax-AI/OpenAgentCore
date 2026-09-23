import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AgentCoreError } from "@agents-core-web/agents-client";

import {
  EnvironmentFilesPanel,
  environmentFilesFailureMessage,
  environmentFilesRequestDirectory,
  formatFileSize,
  validEnvironmentFilesDirectory,
} from "./EnvironmentFilesPanel";

describe("EnvironmentFilesPanel", () => {
  it("accepts only canonical directories inside the Environment Workspace root", () => {
    expect(validEnvironmentFilesDirectory("/workspace")).toBe(true);
    expect(validEnvironmentFilesDirectory("/workspace/project")).toBe(true);
    expect(validEnvironmentFilesDirectory("/workspace/project/src/")).toBe(true);
    expect(validEnvironmentFilesDirectory("/workspace-two")).toBe(false);
    expect(validEnvironmentFilesDirectory("/executor/workspace")).toBe(false);
    expect(validEnvironmentFilesDirectory("/workspace/project/../secret")).toBe(false);
    expect(validEnvironmentFilesDirectory("/workspace/project\\secret")).toBe(false);
    expect(validEnvironmentFilesDirectory("relative")).toBe(false);
    expect(validEnvironmentFilesDirectory(`/workspace/project/${"界".repeat(1_360)}`)).toBe(false);
    expect(validEnvironmentFilesDirectory("/test", "/test")).toBe(true);
    expect(validEnvironmentFilesDirectory("/test/project", "/test")).toBe(true);
    expect(validEnvironmentFilesDirectory("/workspace", "/test")).toBe(false);
    expect(validEnvironmentFilesDirectory("/test/../secret", "/test")).toBe(false);
  });

  it("sends the cleaned directory form that Core requires", () => {
    expect(environmentFilesRequestDirectory("/workspace/project/src/")).toBe("/workspace/project/src");
    expect(environmentFilesRequestDirectory("/workspace//")).toBe("/workspace");
    expect(environmentFilesRequestDirectory("/workspace/project")).toBe("/workspace/project");
  });

  it("reports an unsupported Core instead of a generic directory failure", () => {
    expect(environmentFilesFailureMessage(
      new AgentCoreError("This API operation is not supported.", 404, "unsupported_operation"),
      false,
    )).toContain("not supported by the connected Core");
    expect(environmentFilesFailureMessage(
      new AgentCoreError("Environment not found.", 404, "not_found"),
      false,
    )).toBe("Core could not list this Workspace directory. No partial result was accepted.");
  });

  it("formats exact byte metadata without implying file contents", () => {
    expect(formatFileSize(0)).toBe("0 B");
    expect(formatFileSize(1024)).toBe("1.0 KB");
    expect(formatFileSize(10 * 1024)).toBe("10 KB");
    expect(formatFileSize(1024 * 1024)).toBe("1.0 MB");
  });

  it("renders a request-driven, read-only file listing surface", () => {
    const html = renderToStaticMarkup(
      <EnvironmentFilesPanel
        environmentId="environment_01"
        workspaceDirectory="/test"
        onListFiles={async () => ({ object: "page", data: [], next: null, has_more: false })}
      />,
    );

    expect(html).toContain("Workspace files");
    expect(html).toContain("Direct regular files · metadata only");
    expect(html).toContain('value="/test"');
    expect(html).toContain("List files");
    expect(html).toContain("Listing never starts a Turn or reads file contents");
    expect(html).toContain("Workspace root <code>/test</code>");
    expect(html).toContain("selected directory is sent to Core");
    expect(html).not.toContain("file://");
    expect(html).not.toContain("Download");
    expect(html).not.toContain("Upload");
  });
});
