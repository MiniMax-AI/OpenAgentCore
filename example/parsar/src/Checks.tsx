import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { tr } from "./lib/i18n";
import { product } from "./lib/product";
import { Button } from "./components/ui/button";
import { cn } from "./lib/utils";

const seconds = (ms: number) => (ms / 1000).toFixed(1);

// One tiny generation with the saved connection, so a wrong key or model ID
// shows up here instead of in a conversation.
export function ModelTest({ id, name }: { id: string; name: string }) {
  const test = useMutation({
    mutationFn: () =>
      product<{ ok: boolean; latency_ms: number; error?: string }>(`models/${id}/test`, "POST", {}),
  });
  const result = test.data;
  return (
    <>
      {result && (
        <span
          role="status"
          className={cn("max-w-xs truncate text-sm", result.ok ? "text-success" : "text-danger")}
          title={result.error}
        >
          {result.ok
            ? tr("可用 · {{value0}} 秒", { value0: seconds(result.latency_ms) })
            : result.error}
        </span>
      )}
      <Button
        variant="ghost"
        aria-label={tr("测试 {{value0}}", { value0: name })}
        disabled={test.isPending}
        onClick={() => test.mutate()}
      >
        {test.isPending ? tr("测试中\u2026") : tr("测试")}
      </Button>
    </>
  );
}

type Trial = {
  state: "running" | "succeeded" | "failed";
  started_at: number;
  seconds?: number;
  failure?: string;
  agent_name?: string;
};

// A real conversation with an Agent that uses the template: setup, first reply, cleanup.
export function RuntimeTrial({ id, name }: { id: string; name: string }) {
  const cache = useQueryClient();
  const trial = useQuery({
    queryKey: ["runtime-trial", id],
    queryFn: () => product<Trial | null>(`runtimes/${id}/trial`),
    refetchInterval: (query) => (query.state.data?.state === "running" ? 1500 : false),
  });
  const start = useMutation({
    mutationFn: () => product<Trial>(`runtimes/${id}/trial`, "POST", {}),
    onSuccess: (next) => cache.setQueryData(["runtime-trial", id], next),
  });
  const state = trial.data;
  const running = state?.state === "running" || start.isPending;
  const text = start.error
    ? (start.error as Error).message
    : state?.state === "running"
      ? tr("试运行中\u2026")
      : state?.state === "succeeded"
        ? tr("可用 · 首次回复 {{value0}} 秒", { value0: state.seconds })
        : state?.failure;
  return (
    <>
      {text && (
        <span
          role="status"
          className={cn(
            "max-w-xs truncate text-sm",
            start.error || state?.state === "failed"
              ? "text-danger"
              : state?.state === "succeeded"
                ? "text-success"
                : "text-fg",
          )}
          title={text}
        >
          {text}
        </span>
      )}
      <Button
        variant="ghost"
        aria-label={tr("试运行 {{value0}}", { value0: name })}
        disabled={running}
        onClick={() => start.mutate()}
      >
        {tr("试运行")}
      </Button>
    </>
  );
}
