import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { tr } from "./lib/i18n";
import { product, type SessionRecord } from "./lib/product";
import { Button } from "./components/ui/button";
import { ErrorNotice } from "./components/shared";

const stageText = {
  preparing: "正在准备工作环境",
  starting: "正在启动",
  retrying: "响应较慢，正在自动重试",
} as const;

// Where a running Turn spends its time before the first output, and a plain
// explanation with a retry when it failed.
export function TurnStatus({ record }: { record: SessionRecord }) {
  const cache = useQueryClient();
  const turn = record.turn;
  const [now, setNow] = useState(Date.now());
  const waiting =
    turn?.outcome === "running" &&
    (turn.stage === "preparing" || turn.stage === "starting" || turn.stage === "retrying");
  useEffect(() => {
    if (!waiting) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [waiting]);
  const retry = useMutation({
    mutationFn: () => product<SessionRecord>(`sessions/${record.id}/retry`, "POST", {}),
    onSuccess: (next) => {
      cache.setQueryData(["session-record", record.id], next);
      void cache.invalidateQueries({ queryKey: ["session"] });
    },
  });
  if (!turn) return null;
  if (turn.outcome === "failed" && turn.failure)
    return (
      <div className="mx-auto w-full max-w-3xl px-6 pt-3">
        <div
          role="alert"
          className="flex items-center justify-between gap-4 rounded-xl border border-danger-border bg-danger-subtle px-4 py-3 text-sm text-danger"
        >
          <span>{turn.failure}</span>
          <Button
            variant="outline"
            className="shrink-0"
            disabled={retry.isPending}
            onClick={() => retry.mutate()}
          >
            {retry.isPending ? tr("重试中\u2026") : tr("重试")}
          </Button>
        </div>
        <ErrorNotice error={retry.error} className="mb-0" />
      </div>
    );
  if (!waiting) return null;
  const elapsed = Math.max(0, Math.round((now - turn.started_at) / 1000));
  const text =
    turn.stage === "retrying"
      ? tr(stageText.retrying)
      : turn.slow
        ? tr("比平时慢，请稍候")
        : tr(stageText[turn.stage as keyof typeof stageText]);
  return (
    <div className="mx-auto w-full max-w-3xl px-6 pt-3">
      <p
        role="status"
        className="rounded-xl bg-surface-subtle px-4 py-3 text-sm text-fg"
      >
        {text} · {tr("已等待 {{value0}} 秒", { value0: elapsed })}
        {turn.stage !== "retrying" &&
          ` · ${tr("通常约 {{value0}} 秒", { value0: turn.expected_s })}`}
      </p>
    </div>
  );
}
