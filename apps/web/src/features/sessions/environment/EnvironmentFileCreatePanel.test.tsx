import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import type { AgentCore, EnvironmentFile } from "@agents-core-web/agents-client";

import { EnvironmentFileCreatePanel } from "./EnvironmentFileCreatePanel";
import {
  arrayBufferToStandardBase64,
  createEnvironmentFileCreateGate,
  inlineEnvironmentFileReadFailure,
  inlineEnvironmentFileRequestFailure,
  maxInlineEnvironmentFileBytes,
  submitInlineEnvironmentFile,
  validInlineEnvironmentFileByteLength,
  validInlineEnvironmentFilePath,
} from "./environment-file-create";

const environmentId = "environment-hosted-01";

function created(path: string, size: number): EnvironmentFile {
  return {
    environment_id: environmentId,
    object: "agent.environment.file",
    path,
    size_bytes: size,
  };
}

describe("EnvironmentFileCreatePanel", () => {
  it("accepts only canonical absolute file paths beneath /workspace", () => {
    expect(validInlineEnvironmentFilePath("/workspace/input.txt")).toBe(true);
    expect(validInlineEnvironmentFilePath("/workspace/project/界.txt")).toBe(true);
    expect(validInlineEnvironmentFilePath("/workspace/.hidden")).toBe(true);

    expect(validInlineEnvironmentFilePath("/workspace")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace/")).toBe(false);
    expect(validInlineEnvironmentFilePath("relative.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/other/input.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace/../secret.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace/./input.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace//input.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace/project\\input.txt")).toBe(false);
    expect(validInlineEnvironmentFilePath("/workspace/project/")).toBe(false);
    expect(validInlineEnvironmentFilePath(`/workspace/${"界".repeat(1_363)}`)).toBe(false);
  });

  it("enforces the decoded 5 MiB boundary and encodes multibyte bytes as standard Base64", () => {
    expect(maxInlineEnvironmentFileBytes).toBe(5 * 1024 * 1024);
    expect(validInlineEnvironmentFileByteLength(maxInlineEnvironmentFileBytes)).toBe(true);
    expect(validInlineEnvironmentFileByteLength(maxInlineEnvironmentFileBytes + 1)).toBe(false);
    expect(validInlineEnvironmentFileByteLength(-1)).toBe(false);

    const bytes = new TextEncoder().encode("你好🌍");
    const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    expect(arrayBufferToStandardBase64(buffer)).toBe("5L2g5aW98J+MjQ==");
  });

  it("sends the exact inline payload once and clears the draft after confirmed metadata", async () => {
    const file = new File(["你好"], "greeting.txt", { type: "text/plain" });
    const path = "/workspace/greeting.txt";
    const response = created(path, 6);
    const onCreateFile = vi.fn<AgentCore["createEnvironmentFile"]>(async () => response);
    const result = await submitInlineEnvironmentFile(
      createEnvironmentFileCreateGate(),
      environmentId,
      { file, path },
      onCreateFile,
    );

    expect(onCreateFile).toHaveBeenCalledOnce();
    expect(onCreateFile).toHaveBeenCalledWith(environmentId, {
      type: "inline",
      data: "5L2g5aW9",
      path,
    });
    expect(result).toEqual({ kind: "success", draft: { file: null, path: "" }, file: response });
  });

  it("preserves selection and path after local read failure without sending a request", async () => {
    const read = vi.fn(async () => { throw new DOMException("private local detail", "NotReadableError"); });
    const file = { name: "locked.txt", size: 4, arrayBuffer: read } as unknown as File;
    const draft = { file, path: "/workspace/locked.txt" };
    const onCreateFile = vi.fn<AgentCore["createEnvironmentFile"]>();
    const result = await submitInlineEnvironmentFile(
      createEnvironmentFileCreateGate(),
      environmentId,
      draft,
      onCreateFile,
    );

    expect(read).toHaveBeenCalledOnce();
    expect(onCreateFile).not.toHaveBeenCalled();
    expect(result).toEqual({ kind: "read_failure", draft, message: inlineEnvironmentFileReadFailure });
    expect(inlineEnvironmentFileReadFailure).not.toContain("private local detail");
  });

  it("does not retry a failed Core write and preserves the complete explicit draft", async () => {
    const file = new File(["payload"], "retry.txt");
    const draft = { file, path: "/workspace/retry.txt" };
    const onCreateFile = vi.fn<AgentCore["createEnvironmentFile"]>(async () => {
      throw new Error("private transport detail");
    });
    const result = await submitInlineEnvironmentFile(
      createEnvironmentFileCreateGate(),
      environmentId,
      draft,
      onCreateFile,
    );

    expect(onCreateFile).toHaveBeenCalledOnce();
    expect(result).toEqual({ kind: "request_failure", draft, message: inlineEnvironmentFileRequestFailure });
    expect(inlineEnvironmentFileRequestFailure).not.toContain("private transport detail");
  });

  it("locks duplicate submission before the asynchronous local read completes", async () => {
    let resolveRead: ((value: ArrayBuffer) => void) | undefined;
    const read = vi.fn(() => new Promise<ArrayBuffer>((resolve) => { resolveRead = resolve; }));
    const file = { name: "pending.txt", size: 1, arrayBuffer: read } as unknown as File;
    const draft = { file, path: "/workspace/pending.txt" };
    const gate = createEnvironmentFileCreateGate();
    const onCreateFile = vi.fn<AgentCore["createEnvironmentFile"]>(async () => created(draft.path, 1));

    const first = submitInlineEnvironmentFile(gate, environmentId, draft, onCreateFile);
    const duplicate = await submitInlineEnvironmentFile(gate, environmentId, draft, onCreateFile);

    expect(duplicate).toEqual({ kind: "ignored", draft });
    expect(read).toHaveBeenCalledOnce();
    expect(onCreateFile).not.toHaveBeenCalled();

    resolveRead?.(new Uint8Array([97]).buffer);
    await expect(first).resolves.toEqual({
      kind: "success",
      draft: { file: null, path: "" },
      file: created(draft.path, 1),
    });
    expect(onCreateFile).toHaveBeenCalledOnce();
  });

  it("renders no eligibility inference and states the runtime boundary", () => {
    const html = renderToStaticMarkup(
      <EnvironmentFileCreatePanel
        environmentId={environmentId}
        workspaceDirectory="/workspace"
        onCreateFile={async () => created("/workspace/input.txt", 1)}
      />,
    );

    expect(html).toContain("Add inline Workspace file");
    expect(html).toContain('type="file"');
    expect(html).toContain('placeholder="/workspace/input.txt"');
    expect(html).toContain("does not start a Turn or prove hosted runtime or executor readiness");
    expect(html).not.toContain("openai_hosted");
    expect(html).not.toContain("ready to run");
  });
});
