import type {
  AgentEnvironmentInput,
  CreateSessionInput,
  InlineAgentInput,
  InputMessage,
} from "@agents-core-web/agents-client";

import { createIdempotencyKey } from "../../../lib/pending-send";

export interface SessionCreateDraft {
  agentId?: string;
  sandboxNodeId?: string;
  agent?: InlineAgentInput;
  environment: AgentEnvironmentInput;
  input?: string | InputMessage[];
  metadata: Record<string, string>;
  stream: boolean;
  vaultIds?: string[];
}

export interface SessionCreateAttempt {
  fingerprint: string;
  idempotencyKey: string;
}

function stableJsonValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableJsonValue);
  if (value === null || typeof value !== "object") return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0)
      .map(([key, entry]) => [key, stableJsonValue(entry)]),
  );
}

export function sessionCreateRequestPayload(
  draft: SessionCreateDraft,
): Omit<CreateSessionInput, "stream"> {
  return {
    ...(draft.agentId === undefined ? {} : { agent_id: draft.agentId }),
    ...(draft.agent === undefined ? {} : { agent: draft.agent }),
    environment: draft.environment,
    ...(draft.sandboxNodeId ? { x_agents_core: { sandbox_node_id: draft.sandboxNodeId } } : {}),
    ...(draft.input === undefined ? {} : { input: draft.input }),
    metadata: draft.metadata,
    vault_ids: [...(draft.vaultIds ?? [])].sort(),
  };
}

export function sessionCreateFingerprint(draft: SessionCreateDraft): string {
  const request = {
    ...sessionCreateRequestPayload(draft),
    stream: draft.stream,
  };
  return JSON.stringify(stableJsonValue(request));
}

export function beginSessionCreateAttempt(
  draft: SessionCreateDraft,
  previous: SessionCreateAttempt | null,
  makeKey: () => string = createIdempotencyKey,
): SessionCreateAttempt {
  const fingerprint = sessionCreateFingerprint(draft);
  return {
    fingerprint,
    idempotencyKey: previous?.fingerprint === fingerprint ? previous.idempotencyKey : makeKey(),
  };
}
