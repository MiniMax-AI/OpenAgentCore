import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { tr } from "./lib/i18n";
import { product } from "./lib/product";
import { Check } from "lucide-react";
import { Button } from "./components/ui/button";

// The button carries the result; only a failure adds text, since it says what to fix.
function Failure({ text }: { text?: string }) {
  return text ? (
    <span role="status" className="max-w-xs truncate text-sm text-danger" title={text}>
      {text}
    </span>
  ) : null;
}

const passed = (
  <>
    <Check className="text-success" />
    {tr("可用")}
  </>
);

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
      <Failure text={result && !result.ok ? result.error : undefined} />
      <Button
        variant="ghost"
        aria-label={tr("测试 {{value0}}", { value0: name })}
        disabled={test.isPending}
        onClick={() => test.mutate()}
      >
        {test.isPending ? tr("测试中\u2026") : result?.ok ? passed : tr("测试")}
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
  return (
    <>
      <Failure
        text={start.error ? (start.error as Error).message : state?.state === "failed" ? state.failure : undefined}
      />
      <Button
        variant="ghost"
        aria-label={tr("试运行 {{value0}}", { value0: name })}
        disabled={running}
        onClick={() => start.mutate()}
      >
        {running ? tr("试运行中\u2026") : state?.state === "succeeded" ? passed : tr("试运行")}
      </Button>
    </>
  );
}
