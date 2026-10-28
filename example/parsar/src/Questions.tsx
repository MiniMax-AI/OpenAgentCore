import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { randomUuid, type AgentSession } from "@oac/agents-client";
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
        key: randomUuid(),
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
    },
  });
  return (
    <form
      className="my-6 space-y-4 rounded-2xl border border-line bg-surface p-5 app-shadow-card"
      onSubmit={(e) => {
        e.preventDefault();
        send.mutate();
      }}
    >
      <h3 className="text-base font-semibold">
        {typeof args?.question === "string" ? args.question : action.name}
      </h3>
      {action.name !== "ask_user" && (
        <pre className="max-h-40 overflow-auto text-sm text-fg-muted">
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
