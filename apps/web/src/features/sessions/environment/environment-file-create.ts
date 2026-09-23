import type {
  AgentCore,
  EnvironmentFile,
} from "@agents-core-web/agents-client";

// Core applies the official 5 MiB decoded bound to inline data; Source File copies keep 50 MiB.
export const maxInlineEnvironmentFileBytes = 5 * 1024 * 1024;

export const inlineEnvironmentFileReadFailure =
  "The selected local file could not be read. The selection and destination are unchanged; nothing was sent to Core.";
export const inlineEnvironmentFileRequestFailure =
  "Core did not confirm the file write. The selection and destination are unchanged, and Web did not retry the request.";

export interface EnvironmentFileCreateDraft {
  file: File | null;
  path: string;
}

export interface EnvironmentFileCreateGate {
  pending: boolean;
}

export type EnvironmentFileCreateAttemptResult =
  | { kind: "success"; draft: EnvironmentFileCreateDraft; file: EnvironmentFile }
  | { kind: "validation_error"; draft: EnvironmentFileCreateDraft; message: string }
  | { kind: "read_failure"; draft: EnvironmentFileCreateDraft; message: string }
  | { kind: "request_failure"; draft: EnvironmentFileCreateDraft; message: string }
  | { kind: "ignored"; draft: EnvironmentFileCreateDraft };

export function createEnvironmentFileCreateGate(): EnvironmentFileCreateGate {
  return { pending: false };
}

/**
 * Mirrors the Agent Core client's canonical Environment file path boundary.
 * The value must name a file below /workspace, not the directory itself.
 */
export function validInlineEnvironmentFilePath(value: string): boolean {
  if (
    !value.startsWith("/workspace/") ||
    value.length <= "/workspace/".length ||
    new TextEncoder().encode(value).length > 4096 ||
    value.includes("\\") ||
    value.includes("\0") ||
    value.includes("\r") ||
    value.includes("\n") ||
    value.split("/").includes("..")
  ) return false;

  const components = value.split("/").filter((component) => component && component !== ".");
  return `/${components.join("/")}` === value;
}

export function validInlineEnvironmentFileByteLength(bytes: number): boolean {
  return Number.isSafeInteger(bytes) && bytes >= 0 && bytes <= maxInlineEnvironmentFileBytes;
}

/**
 * Encodes exact bytes with the standard RFC 4648 alphabet. Chunks are a
 * multiple of three so concatenating their Base64 output cannot introduce
 * padding in the middle of the result.
 */
export function arrayBufferToStandardBase64(buffer: ArrayBuffer): string {
  if (!validInlineEnvironmentFileByteLength(buffer.byteLength)) {
    throw new RangeError("Inline Environment files must be at most 5 MiB.");
  }

  const bytes = new Uint8Array(buffer);
  const chunkBytes = 3 * 8192;
  let encoded = "";
  for (let offset = 0; offset < bytes.length; offset += chunkBytes) {
    const end = Math.min(offset + chunkBytes, bytes.length);
    let binary = "";
    for (let index = offset; index < end; index += 1) {
      binary += String.fromCharCode(bytes[index] ?? 0);
    }
    encoded += btoa(binary);
  }
  return encoded;
}

/**
 * Performs exactly one explicit local read followed by at most one Core write.
 * The Base64 string stays local to this call and is neither logged nor returned.
 */
export async function submitInlineEnvironmentFile(
  gate: EnvironmentFileCreateGate,
  environmentId: string,
  draft: EnvironmentFileCreateDraft,
  onCreateFile: AgentCore["createEnvironmentFile"],
): Promise<EnvironmentFileCreateAttemptResult> {
  if (gate.pending) return { kind: "ignored", draft };
  if (!draft.file) {
    return { kind: "validation_error", draft, message: "Choose one local file before writing." };
  }
  if (!validInlineEnvironmentFilePath(draft.path)) {
    return {
      kind: "validation_error",
      draft,
      message: "Use one canonical absolute file path beneath /workspace/.",
    };
  }
  if (!validInlineEnvironmentFileByteLength(draft.file.size)) {
    return {
      kind: "validation_error",
      draft,
      message: "The selected file must contain at most 5 MiB.",
    };
  }

  gate.pending = true;
  try {
    let bytes: ArrayBuffer;
    try {
      bytes = await draft.file.arrayBuffer();
    } catch {
      return { kind: "read_failure", draft, message: inlineEnvironmentFileReadFailure };
    }

    if (!validInlineEnvironmentFileByteLength(bytes.byteLength)) {
      return {
        kind: "validation_error",
        draft,
        message: "The decoded file content must contain at most 5 MiB.",
      };
    }

    const data = arrayBufferToStandardBase64(bytes);
    try {
      const file = await onCreateFile(environmentId, {
        type: "inline",
        data,
        path: draft.path,
      });
      return { kind: "success", draft: { file: null, path: "" }, file };
    } catch {
      return { kind: "request_failure", draft, message: inlineEnvironmentFileRequestFailure };
    }
  } finally {
    gate.pending = false;
  }
}
