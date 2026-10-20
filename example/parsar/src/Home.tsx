import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowUp, Plus } from "lucide-react";
import {
  product,
  ProductError,
  useAgents,
  type AgentProfile,
  type SessionRecord,
} from "./lib/product";
import { Button } from "./components/ui/button";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import { ErrorNotice } from "./components/shared";
import { AgentAvatar } from "./components/AgentAvatar";
import { cn } from "./lib/utils";

export function Home({ agentId }: { agentId?: string }) {
  const agents = useAgents();
  const cache = useQueryClient();
  const available = (agents.data || []).filter(
    (agent) => agent.enabled !== false,
  );
  const [chosen, setChosen] = useState(agentId || "");
  const agent =
    available.find((row) => row.id === chosen) || available[0] || undefined;
  const [text, setText] = useState("");
  const start = useMutation({
    mutationFn: async (id: string) =>
      product<SessionRecord>(`sessions/${id}`, "PUT", {
        name: text.trim().split("\n")[0]!.slice(0, 60),
        agent_id: agent!.id,
        input: text.trim(),
      }),
    onSuccess: (session) => {
      void cache.invalidateQueries({ queryKey: ["sessions"] });
      location.hash = `/sessions/${session.id}`;
    },
    onError: (error, id) => {
      // An unconfirmed creation keeps its frozen request; its page offers recovery.
      if (
        !(
          error instanceof ProductError &&
          [400, 401, 403, 404, 413, 422].includes(error.status)
        )
      ) {
        void cache.invalidateQueries({ queryKey: ["sessions"] });
        location.hash = `/sessions/${id}`;
      }
    },
  });
  const submit = () => {
    if (agent && text.trim() && !start.isPending)
      start.mutate(crypto.randomUUID());
  };
  if (!agents.isPending && !available.length)
    return (
      <div className="flex flex-1 items-center justify-center px-6">
        <EmptyState
          title={tr("先创建一个 Agent")}
          description={tr("Agent 决定由谁来工作、用什么工具，以及在什么环境里运行。")}
          action={
            <Button asChild size="lg">
              <a href="#/agents/new">
                <Plus />
                {tr("新建 Agent")}
              </a>
            </Button>
          }
        />
      </div>
    );
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6 pb-24 pt-16">
      <div className="w-full max-w-2xl animate-page-in">
        <h1 className="text-center text-3xl font-semibold tracking-tight text-fg">
          {tr("今天想完成什么？")}
        </h1>
        <form
          className="mt-10 rounded-3xl border border-line bg-surface p-3 app-shadow-floating transition-shadow focus-within:border-line-strong"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <textarea
            autoFocus
            aria-label={tr("描述你想完成的工作")}
            rows={3}
            value={text}
            disabled={start.isPending}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (
                event.key === "Enter" &&
                !event.shiftKey &&
                !event.nativeEvent.isComposing
              ) {
                event.preventDefault();
                submit();
              }
            }}
            placeholder={
              agent
                ? tr("交给 {{name}}…", { name: agent.name })
                : tr("描述你想完成的工作…")
            }
            className="block max-h-72 min-h-24 w-full resize-none bg-transparent px-3 pt-2 text-base leading-relaxed text-fg outline-none placeholder:text-fg-subtle"
          />
          <div className="flex items-center justify-between gap-3 pt-2">
            {available.length > 0 && agent && (
              <Select
                aria-label={tr("选择 Agent")}
                wrapperClassName="w-auto max-w-[60%]"
                className="h-9 rounded-full border-transparent bg-surface-muted px-4 shadow-none hover:app-pressed"
                value={agent.id}
                onValueChange={setChosen}
              >
                {available.map((row) => (
                  <SelectOption key={row.id} value={row.id}>
                    {row.name}
                  </SelectOption>
                ))}
              </Select>
            )}
            <button
              type="submit"
              aria-label={tr("发送")}
              disabled={!agent || !text.trim() || start.isPending}
              className="ml-auto flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-emphasis text-fg-on-emphasis transition-opacity disabled:opacity-30"
            >
              <ArrowUp className="h-[18px] w-[18px]" strokeWidth={2} />
            </button>
          </div>
        </form>
        <ErrorNotice error={start.error || agents.error} className="mt-4" />
        {available.length > 1 && (
          <div className="mt-10 grid gap-3 sm:grid-cols-2">
            {available.slice(0, 4).map((row) => (
              <AgentChoice
                key={row.id}
                agent={row}
                selected={row.id === agent?.id}
                choose={() => setChosen(row.id)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function AgentChoice({
  agent,
  selected,
  choose,
}: {
  agent: AgentProfile;
  selected: boolean;
  choose: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={choose}
      className={cn(
        "flex items-start gap-3.5 rounded-2xl border p-4 text-left transition-colors",
        selected
          ? "border-line-strong bg-surface app-shadow-card"
          : "border-line bg-surface-subtle/60 hover:bg-surface",
      )}
    >
      <AgentAvatar name={agent.name} />
      <span className="min-w-0">
        <span className="block truncate text-sm font-medium text-fg">
          {agent.name}
        </span>
        <span className="mt-0.5 line-clamp-2 text-sm text-fg-muted">
          {agent.description || tr("暂无描述")}
        </span>
      </span>
    </button>
  );
}
