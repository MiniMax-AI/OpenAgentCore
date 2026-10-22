import { useMutation, useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { tr } from "./lib/i18n";
import { product } from "./lib/product";
import { dateTime } from "./lib/api";
import { Button } from "./components/ui/button";
import { ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";

type Artifact = {
  id: string;
  path: string;
  size_bytes: number;
  created_at: number;
  session_id: string;
  core_session_id: string;
  conversation: string;
};

const fileName = (path: string) => path.replace(/^\/workspace\/outputs\//, "");

// Every file recent conversations produced, so outputs are found without
// remembering which conversation made them.
export function Artifacts() {
  const query = useQuery({
    queryKey: ["all-artifacts"],
    queryFn: () => product<Artifact[]>("artifacts"),
  });
  const download = useMutation({
    mutationFn: async (file: Artifact) => {
      const response = await fetch(
        `/v1/agents/sessions/${encodeURIComponent(file.core_session_id)}/artifacts/${encodeURIComponent(file.id)}/content`,
      );
      if (!response.ok) {
        const value = await response.json().catch(() => ({}));
        throw new Error(value.error?.message || tr("下载失败，请重试。"));
      }
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = file.path.split("/").at(-1) || "artifact";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
  });
  const rows = query.data || [];
  return (
    <>
      <PageHeader title={tr("产物")} />
      <ErrorNotice page error={query.error || download.error} />
      <div className="mx-auto w-full max-w-6xl px-6 pb-12 pt-1 sm:px-10">
        {rows.length ? (
          <div className="divide-y divide-line-muted overflow-hidden rounded-2xl border border-line bg-surface app-shadow-card">
            {rows.map((file) => (
              <article key={`${file.core_session_id}:${file.id}`} className="flex min-h-[72px] items-center gap-4 px-6 py-4">
                <div className="min-w-0 flex-1">
                  <h2 className="break-all text-sm font-semibold text-fg">{fileName(file.path)}</h2>
                  <p className="mt-0.5 truncate text-sm text-fg-muted">
                    <a href={`#/sessions/${file.session_id}`} className="text-fg underline-offset-4 hover:underline">
                      {file.conversation}
                    </a>
                    {" · "}
                    {Math.ceil(file.size_bytes / 1024)} KiB · {dateTime(file.created_at)}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={download.isPending}
                  onClick={() => download.mutate(file)}
                  aria-label={tr("下载 {{value0}}", { value0: fileName(file.path) })}
                >
                  <Download />
                </Button>
              </article>
            ))}
          </div>
        ) : (
          !query.isPending && <EmptyState title={tr("还没有生成的文件")} />
        )}
      </div>
    </>
  );
}
