import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AgentCoreError,
  createIdempotencyKey,
  type AgentSession,
} from "@oac/agents-client";
import { ArrowUp, Square } from "lucide-react";
import { beginTiming, requestReturned } from "./lib/session-timing";
import { api } from "./lib/api";
import { ErrorNotice } from "./components/shared";

export function Composer({
  session,
  turnId,
  connectionPending = false,
  attachment = "",
  onPendingChange,
}: {
  session: AgentSession;
  turnId?: string;
  connectionPending?: boolean;
  attachment?: string;
  onPendingChange?: (pending: boolean) => void;
}) {
  const cache = useQueryClient();
  const storageKey = `oac-example-message-${session.id}`;
  const [pending, setPending] = useState<{ key: string; text: string } | null>(
    () => {
      try {
        return JSON.parse(sessionStorage.getItem(storageKey) || "null");
      } catch {
        return null;
      }
    },
  );
  useEffect(() => {
    onPendingChange?.(Boolean(pending));
  }, [pending, onPendingChange]);
  const draftKey = `oac-example-draft-${session.id}`;
  const [text, setText] = useState(
    () => pending?.text || sessionStorage.getItem(draftKey) || "",
  );
  useEffect(() => {
    if (text) sessionStorage.setItem(draftKey, text);
    else sessionStorage.removeItem(draftKey);
  }, [draftKey, text]);
  useEffect(() => {
    if (attachment)
      setText(
        (previous) =>
          `${previous}${previous ? "\n" : ""}请处理文件：${attachment}`,
      );
  }, [attachment]);
  const [cancelKey, setCancelKey] = useState(createIdempotencyKey);
  const [cancelRequested, setCancelRequested] = useState(false);
  useEffect(() => {
    setCancelKey(createIdempotencyKey());
    setCancelRequested(false);
  }, [turnId]);
  const running =
    session.status === "in_progress" || session.status === "requires_action";
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ["session", session.id] });
    void cache.invalidateQueries({ queryKey: ["sessions"] });
  };
  const send = useMutation({
    mutationFn: async () => {
      const operation = pending || { key: createIdempotencyKey(), text };
      sessionStorage.setItem(storageKey, JSON.stringify(operation));
      setPending(operation);
      beginTiming(session.id, operation.key, turnId);
      let response: Promise<void>;
      try {
        response = api.sendMessage(session.id, operation.text, operation.key);
      } catch (error) {
        // Client validation throws before sending; network failures reject the Promise.
        sessionStorage.removeItem(storageKey);
        setPending(null);
        throw error;
      }
      await response;
      requestReturned(session.id, operation.key);
    },
    onSuccess: () => {
      sessionStorage.removeItem(draftKey);
      setText("");
      setPending(null);
      sessionStorage.removeItem(storageKey);
      refresh();
    },
    onError: (error) => {
      if (
        error instanceof AgentCoreError &&
        [400, 401, 403, 404, 413, 422].includes(error.status)
      ) {
        sessionStorage.removeItem(storageKey);
        setPending(null);
      }
    },
  });
  const cancel = useMutation({
    mutationFn: () => api.cancelTurn(session.id, cancelKey),
    onSuccess: () => {
      setCancelRequested(true);
      refresh();
    },
  });
  return (
    <div className="mx-auto w-full max-w-3xl px-6 pb-6 pt-3">
      <ErrorNotice error={send.error || cancel.error} />
      {cancelRequested && running && (
        <p role="status" className="pb-3 text-base">
          已请求取消，等待执行结束。
        </p>
      )}
      <form
        className="rounded-2xl bg-surface-muted p-4 focus-within:ring-1 focus-within:ring-line-strong"
        onSubmit={(e) => {
          e.preventDefault();
          if (!connectionPending && text.trim() && !running && !send.isPending)
            send.mutate();
        }}
      >
        <textarea
          aria-label="继续对话"
          rows={3}
          value={text}
          disabled={
            running ||
            send.isPending ||
            session.status === "failed" ||
            !!pending
          }
          onChange={(e) => setText(e.target.value)}
          placeholder={
            running
              ? "Agent 执行中"
              : session.status === "failed"
                ? "当前会话已失败，请返回会话列表新建会话"
                : "继续描述你的想法…"
          }
          className="block w-full resize-none bg-transparent text-base leading-relaxed outline-none placeholder:text-fg-muted disabled:opacity-60"
        />
        <div className="flex justify-end">
          {running ? (
            <button
              type="button"
              aria-label="取消执行"
              disabled={cancel.isPending || cancelRequested}
              onClick={() => cancel.mutate()}
              className="flex h-9 w-9 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis disabled:opacity-40"
            >
              <Square className="h-4 w-4" fill="currentColor" />
            </button>
          ) : (
            <button
              type="submit"
              aria-label={pending ? "重试发送" : "发送"}
              disabled={
                connectionPending ||
                !text.trim() ||
                send.isPending ||
                session.status === "failed"
              }
              className="flex h-9 w-9 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis disabled:opacity-40"
            >
              <ArrowUp className="h-5 w-5" />
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
