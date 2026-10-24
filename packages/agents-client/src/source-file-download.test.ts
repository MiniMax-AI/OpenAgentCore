import { describe, expect, it, vi } from "vitest";

import { AgentCoreError, OpenAIAgentsClient } from "./client";

const fileId = "file-16e1f26e-8cf6-4272-9c31-d470b08d31af";

function contentHeaders(bytes = 3): Headers {
  return new Headers({
    "Content-Type": "application/octet-stream",
    "Content-Disposition": 'attachment; filename="notes.txt"',
    "Content-Length": String(bytes),
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
  });
}

function clientFor(response: Response) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
  return { client: new OpenAIAgentsClient({ fetch }), fetch };
}

describe("Source File download body cleanup", () => {
  it.each([
    { header: "Content-Type", value: null },
    { header: "Content-Type", value: "application/json" },
    { header: "Content-Disposition", value: null },
    { header: "Content-Disposition", value: "inline" },
    { header: "Content-Length", value: null },
    { header: "Content-Length", value: "" },
    { header: "Content-Length", value: "03" },
    { header: "Content-Length", value: "-1" },
    { header: "Content-Length", value: "1.5" },
    { header: "Content-Length", value: "9007199254740992" },
    { header: "Content-Length", value: String(512 * 1024 * 1024 + 1) },
    { header: "Cache-Control", value: null },
    { header: "Cache-Control", value: "public" },
    { header: "X-Content-Type-Options", value: null },
    { header: "X-Content-Type-Options", value: "sniff" },
  ])("cancels rejected content with $header=$value", async ({ header, value }) => {
    const headers = contentHeaders();
    if (value === null) headers.delete(header);
    else headers.set(header, value);
    const cancel = vi.fn();
    const response = new Response(new ReadableStream<Uint8Array>({ cancel }, { highWaterMark: 0 }), { headers });
    const { client, fetch } = clientFor(response);

    await expect(client.downloadSourceFile(fileId)).rejects.toMatchObject({
      status: 502,
      code: "invalid_source_file_content",
      message: "OpenAgentCore returned invalid Source File content headers.",
    });
    expect(cancel).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("preserves the typed header error when cancelling the body fails", async () => {
    const headers = contentHeaders();
    headers.delete("Content-Length");
    const cancel = vi.fn().mockRejectedValue(new Error("body cancellation failed"));
    const response = new Response(new ReadableStream<Uint8Array>({ cancel }, { highWaterMark: 0 }), { headers });
    const { client, fetch } = clientFor(response);

    const download = client.downloadSourceFile(fileId);
    await expect(download).rejects.toBeInstanceOf(AgentCoreError);
    await expect(download).rejects.toMatchObject({
      status: 502,
      code: "invalid_source_file_content",
      message: "OpenAgentCore returned invalid Source File content headers.",
    });
    expect(cancel).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("preserves the header error when there is no response body", async () => {
    const headers = contentHeaders();
    headers.delete("Content-Length");
    const { client } = clientFor(new Response(null, { headers }));

    await expect(client.downloadSourceFile(fileId)).rejects.toMatchObject({
      status: 502,
      code: "invalid_source_file_content",
      message: "OpenAgentCore returned invalid Source File content headers.",
    });
  });

  it.each([{ bytes: [] }, { bytes: [1, 2, 3] }])("reads valid content $bytes without cancelling it", async ({ bytes }) => {
    const cancel = vi.fn();
    const response = new Response(new ReadableStream<Uint8Array>({
      pull(controller) {
        controller.enqueue(new Uint8Array(bytes));
        controller.close();
      },
      cancel,
    }, { highWaterMark: 0 }), { headers: contentHeaders(bytes.length) });
    const { client, fetch } = clientFor(response);

    const content = await client.downloadSourceFile(fileId);
    expect(Array.from(content.data)).toEqual(bytes);
    expect(content.bytes).toBe(bytes.length);
    expect(cancel).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledOnce();
  });
});
