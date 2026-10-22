import { useQuery } from "@tanstack/react-query";
import { tr } from "./lib/i18n";
import { product } from "./lib/product";
import { Section } from "./components/shared";

type Health = {
  turns: number;
  success_rate: number | null;
  median_first_output_s: number | null;
  retried: number;
};

// Seven-day reliability and speed, from the Turns Parsar watched for this Agent.
export function AgentHealth({ id }: { id: string }) {
  const health = useQuery({
    queryKey: ["agent-health", id],
    queryFn: () => product<Health>(`agents/${id}/health`),
    refetchInterval: 30000,
  });
  const data = health.data;
  if (!data) return null;
  const stats = [
    [tr("成功率"), data.success_rate === null ? "—" : `${Math.round(data.success_rate * 100)}%`],
    [
      tr("首次回复"),
      data.median_first_output_s === null
        ? "—"
        : tr("{{value0}} 秒", { value0: data.median_first_output_s.toFixed(1) }),
    ],
    [tr("自动恢复"), tr("{{value0}} 次", { value0: data.retried })],
  ];
  return (
    <Section title={tr("近 7 天 · {{value0}} 轮对话", { value0: data.turns })}>
      <dl className="grid grid-cols-3 gap-4">
        {stats.map(([label, value]) => (
          <div key={label} className="rounded-xl bg-surface-subtle px-4 py-4">
            <dt className="text-sm text-fg-muted">{label}</dt>
            <dd className="mt-1 text-2xl font-semibold tabular-nums text-fg">{value}</dd>
          </div>
        ))}
      </dl>
    </Section>
  );
}
