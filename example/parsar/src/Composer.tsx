import { tr } from "./lib/i18n";
import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AgentCoreError,
  randomUuid,
  type AgentSession,
} from "@oac/agents-client";
import { ArrowUp, Square } from "lucide-react";
import { api } from "./lib/api";
import { ErrorNotice } from "./components/shared";
export function Composer({
  session,
  turnId,
  attachment = "",
  onPendingChange,
}: {
  session: AgentSession;
  turnId?: string;
  attachment?: string;
  onPendingChange?: (pending: boolean) => void;
}) {
  const cache = useQueryClient();
  const storageKey = `oac-example-message-${session.id}`;
  const [pending, setPending] = useState<{
    key: string;
    text: string;
  } | null>(() => {
    try {
      return JSON.parse(sessionStorage.getItem(storageKey) || "null");
    } catch {
      return null;
    }
  });
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
      setText((previous) =>
        tr("{{value0}}{{value1}}请处理文件：{{value2}}", {
          value0: previous,
          value1: previous ? "\n" : "",
          value2: attachment,
        }),
      );
  }, [attachment]);
  const [cancelKey, setCancelKey] = useState(randomUuid);
  const [cancelRequested, setCancelRequested] = useState(false);
  useEffect(() => {
    setCancelKey(randomUuid());
    setCancelRequested(false);
  }, [turnId]);
  const running =
    session.status === "in_progress" || session.status === "requires_action";
  // Input during an active Turn reaches the running task; questions are answered on their card.
  const waiting = session.status === "requires_action";
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ["session", session.id] });
    void cache.invalidateQueries({ queryKey: ["sessions"] });
  };
  const send = useMutation({
    mutationFn: async () => {
      const operation = pending || { key: randomUuid(), text };
      sessionStorage.setItem(storageKey, JSON.stringify(operation));
      setPending(operation);
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
    <div className="mx-auto w-full max-w-3xl px-6 pb-7 pt-3">
      <ErrorNotice error={send.error || cancel.error} />
      {cancelRequested && running && (
        <p role="status" className="pb-3 text-sm text-fg-muted">
          {tr("已请求取消，等待执行结束。")}
        </p>
      )}
      <form
        className="rounded-3xl border border-line bg-surface p-3 app-shadow-floating transition-colors focus-within:border-line-strong"
        onSubmit={(e) => {
          e.preventDefault();
          if (text.trim() && !waiting && !send.isPending) send.mutate();
        }}
      >
        <textarea
          aria-label={tr("继续对话")}
          rows={2}
          value={text}
          onKeyDown={(e) => {
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing
            ) {
              e.preventDefault();
              e.currentTarget.form?.requestSubmit();
            }
          }}
          disabled={
            waiting ||
            send.isPending ||
            session.status === "failed" ||
            !!pending
          }
          onChange={(e) => setText(e.target.value)}
          placeholder={
            waiting
              ? tr("请先回答上方的问题")
              : running
                ? tr("补充说明，Agent 会在当前任务中看到\u2026")
                : session.status === "failed"
                  ? tr("当前会话已失败，请开始新对话")
                  : tr("继续对话\u2026")
          }
          className="block max-h-60 w-full resize-none bg-transparent px-3 pt-2 text-base leading-relaxed outline-none placeholder:text-fg-subtle disabled:opacity-60"
        />
        <div className="flex justify-end gap-2 pt-1">
          {running && (
            <button
              type="button"
              aria-label={tr("取消执行")}
              title={tr("停止")}
              disabled={cancel.isPending || cancelRequested}
              onClick={() => cancel.mutate()}
              className={
                text.trim() && !waiting
                  ? "flex h-9 w-9 items-center justify-center rounded-full border border-line-strong text-fg hover:app-hover disabled:opacity-30"
                  : "flex h-9 w-9 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis disabled:opacity-30"
              }
            >
              <Square className="h-4 w-4" fill="currentColor" />
            </button>
          )}
          {(!running || (text.trim() && !waiting)) && (
            <button
              type="submit"
              aria-label={pending ? tr("重试发送") : tr("发送")}
              disabled={
                !text.trim() || send.isPending || session.status === "failed"
              }
              className="flex h-9 w-9 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis disabled:opacity-30"
            >
              <ArrowUp className="h-5 w-5" />
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
