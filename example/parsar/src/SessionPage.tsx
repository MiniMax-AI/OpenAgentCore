import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { product, type SessionRecord } from "./lib/product";
import { SessionDetail } from "./SessionDetail";
import { EmptyState } from "./components/ui/empty-state";
import { Button } from "./components/ui/button";
import { ErrorNotice } from "./components/shared";
export function SessionPage({ id }: { id: string }) {
  const cache = useQueryClient();
  const query = useQuery({
    queryKey: ["session-record", id],
    queryFn: () => product<SessionRecord>(`sessions/${id}`),
  });
  const resume = useMutation({
    mutationFn: () => product<SessionRecord>(`sessions/${id}`, "PUT", {}),
    onSuccess: (record) => {
      cache.setQueryData(["session-record", id], record);
      void cache.invalidateQueries({ queryKey: ["sessions"] });
    },
  });
  if (query.data?.core_session_id)
    return (
      <SessionDetail
        key={query.data.core_session_id}
        id={query.data.core_session_id}
        agentId={query.data.agent_id}
        machine={query.data.self_hosted}
      />
    );
  return (
    <div className="p-6">
      <Button asChild variant="ghost">
        <a href={`#/agents/${query.data?.agent_id || ""}`}>返回 Agent</a>
      </Button>
      <ErrorNotice error={query.error || resume.error} />
      <EmptyState title={query.isPending ? "正在加载…" : "会话创建尚未确认"} />
      {query.data && (
        <Button disabled={resume.isPending} onClick={() => resume.mutate()}>
          {resume.isPending ? "恢复中…" : "恢复创建"}
        </Button>
      )}
    </div>
  );
}
