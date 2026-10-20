import { tr } from "./lib/i18n";
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
  if (query.data?.deleted_at)
    return <EmptyState className="flex-1" title={tr("此会话已删除")} />;
  if (query.data?.core_session_id)
    return (
      <SessionDetail
        key={query.data.core_session_id}
        id={query.data.core_session_id}
        record={query.data}
      />
    );
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-6">
      <ErrorNotice page error={query.error || resume.error} className="max-w-md" />
      <EmptyState
        title={query.isPending ? tr("正在加载\u2026") : tr("会话创建尚未确认")}
        description={
          query.data
            ? tr("请求已保存。恢复会使用同一份请求，不会重复创建。")
            : undefined
        }
        action={
          query.data && (
            <Button disabled={resume.isPending} onClick={() => resume.mutate()}>
              {resume.isPending ? tr("恢复中\u2026") : tr("恢复创建")}
            </Button>
          )
        }
      />
    </div>
  );
}
