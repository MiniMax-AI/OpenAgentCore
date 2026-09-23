import { AgentCoreError } from "@agents-core-web/agents-client";

const TRANSIENT_CLIENT_STATUSES = new Set([408, 409, 425, 429]);

export interface PendingSend {
  sessionId: string;
  payload: string;
  idempotencyKey: string;
}

export interface FailedPendingSend extends PendingSend {
  code?: string;
  message: string;
  uncertain: boolean;
}

export function createIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function isUncertainSendFailure(error: unknown): boolean {
  if (!(error instanceof AgentCoreError)) return true;
  return error.status >= 500 || error.status < 400 || TRANSIENT_CLIENT_STATUSES.has(error.status);
}

export function beginPendingSend(
  sessionId: string,
  payload: string,
  previous: FailedPendingSend | undefined,
  makeKey: () => string = createIdempotencyKey,
): PendingSend {
  const canReuse = previous?.uncertain === true &&
    previous.sessionId === sessionId &&
    previous.payload === payload;
  return {
    sessionId,
    payload,
    idempotencyKey: canReuse ? previous.idempotencyKey : makeKey(),
  };
}

export function failPendingSend(
  pending: PendingSend,
  error: unknown,
  message: string,
): FailedPendingSend {
  return {
    ...pending,
    code: error instanceof AgentCoreError ? error.code ?? undefined : undefined,
    message,
    uncertain: isUncertainSendFailure(error),
  };
}
