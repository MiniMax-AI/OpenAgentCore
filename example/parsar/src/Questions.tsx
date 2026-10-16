import { useQuery } from "@tanstack/react-query";
import { readHistory } from "./lib/api";
import { useSessions } from "./lib/product";
import { PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createIdempotencyKey, type AgentSession } from "@oac/agents-client";
import { api } from "./lib/api";
import { Button } from "./components/ui/button";
import { Textarea } from "./components/ui/textarea";
import { ErrorNotice } from "./components/shared";
export function Questions({ session }: { session: AgentSession }) {
  return (
    <>
      {session.required_actions
        .filter((action) => action.type === "function_call")
        .map((action) => (
          <Question
            key={action.call_id}
            sessionId={session.id}
            action={action}
          />
        ))}
    </>
  );
}
function Question({
  sessionId,
  action,
}: {
  sessionId: string;
  action: Extract<
    AgentSession["required_actions"][number],
    {
      type: "function_call";
    }
  >;
}) {
  const storageKey = `parsar-answer-${sessionId}-${action.call_id}`,
    cache = useQueryClient();
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
  const [answer, setAnswer] = useState(pending?.text || "");
  let args: Record<string, unknown> = {};
  try {
    args =
      typeof action.arguments === "string"
        ? JSON.parse(action.arguments)
        : (action.arguments as Record<string, unknown>);
  } catch {
    /* Display raw arguments below. */
  }
  const options = Array.isArray(args?.options)
    ? args.options.filter((value): value is string => typeof value === "string")
    : [];
  const send = useMutation({
    mutationFn: async () => {
      const operation = pending || {
        key: createIdempotencyKey(),
        text: answer,
      };
      sessionStorage.setItem(storageKey, JSON.stringify(operation));
      setPending(operation);
      await api.submitFunctionResult(
        sessionId,
        {
          callId: action.call_id,
          turnId: action.turn_id,
          success: true,
          output: operation.text,
        },
        operation.key,
      );
    },
    onSuccess: () => {
      sessionStorage.removeItem(storageKey);
      setPending(null);
      void cache.invalidateQueries({ queryKey: ["session", sessionId] });
      void cache.invalidateQueries({ queryKey: ["inbox"] });
    },
  });
  return (
    <form
      className="my-4 space-y-3 border-y border-line py-4"
      onSubmit={(e) => {
        e.preventDefault();
        send.mutate();
      }}
    >
      <h3 className="text-sm font-medium">
        {typeof args?.question === "string" ? args.question : action.name}
      </h3>
      {action.name !== "ask_user" && (
        <pre className="max-h-40 overflow-auto text-xs text-fg-muted">
          {JSON.stringify(action.arguments, null, 2)}
        </pre>
      )}
      {options.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {options.map((option) => (
            <Button
              type="button"
              variant="outline"
              key={option}
              disabled={!!pending}
              onClick={() => setAnswer(option)}
            >
              {option}
            </Button>
          ))}
        </div>
      )}
      <Textarea
        aria-label={tr("回复 Agent 提问")}
        required
        rows={3}
        value={answer}
        disabled={!!pending || send.isPending}
        onChange={(e) => setAnswer(e.target.value)}
      />
      <ErrorNotice error={send.error} />
      <Button type="submit" disabled={send.isPending || !answer.trim()}>
        {pending ? tr("重试原回答") : tr("提交回答")}
      </Button>
    </form>
  );
}

export function Inbox() {
  const records = useSessions();
  const query = useQuery({
    queryKey: ["inbox"],
    queryFn: ({ signal }) =>
      readHistory((options) => api.listSessions(options), signal),
    refetchInterval: 5000,
  });
  const local = new Map(records.data?.map((row) => [row.core_session_id, row]));
  const pending = query.data?.filter(
    (session) =>
      local.has(session.id) &&
      session.required_actions.some(
        (action) => action.type === "function_call",
      ),
  );
  return (
    <>
      <PageHeader title={tr("收件箱")} />
      <ErrorNotice error={records.error || query.error} />
      <div className="min-h-0 flex-1 overflow-auto px-6 py-4">
        {pending?.map((session) => (
          <section
            className="mx-auto max-w-3xl border-b border-line"
            key={session.id}
          >
            <a
              className="text-sm font-medium underline"
              href={`#/sessions/${local.get(session.id)!.id}`}
            >
              {local.get(session.id)!.name}
            </a>
            <Questions session={session} />
          </section>
        ))}
        {!query.isPending && !pending?.length && (
          <EmptyState
            title={tr("没有待回答的问题")}
            description={tr(
              "Agent 需要补充业务信息时会出现在这里。执行始终使用 bypass。",
            )}
          />
        )}
      </div>
    </>
  );
}
