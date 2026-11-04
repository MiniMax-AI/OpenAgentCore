import { tr } from "./lib/i18n";
import { maxUploadBytes } from "../shared/media.mjs";
import { useRef } from "react";
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";
import { Upload, Download } from "lucide-react";
import { randomUuid, type ListPage, type SessionArtifact } from "@oac/agents-client";
import { api, dateTime, downloadArtifact, listSessionArtifacts } from "./lib/api";
import { Button } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { ErrorNotice } from "./components/shared";
async function upload(environmentId: string, file: File) {
  if (!file.size) throw new Error(tr("文件不能为空。"));
  if (file.size > maxUploadBytes) throw new Error(tr("文件不能超过 50 MiB。"));
  let name = "";
  const encoder = new TextEncoder();
  for (const character of file.name.replace(
    /[\\/:*?"<>|\x00-\x1f\x7f]/g,
    "_",
  )) {
    if (encoder.encode(name + character).length > 200) break;
    name += character;
  }
  name ||= "file";
  const path = `/workspace/inputs/${randomUuid()}-${name}`;
  // Core's Files API takes the bytes; the workspace gets the file by its ID.
  const uploaded = await api.uploadSourceFile({ file, filename: name });
  try {
    await api.createEnvironmentFile(environmentId, {
      type: "file_id",
      path,
      file_id: uploaded.id,
    });
  } catch (error) {
    const reason = error instanceof Error ? error.message : tr("请求失败");
    throw new Error(
      tr(
        "{{value0}} 上传未确认，文件可能已写入 {{value1}}。请先检查该路径再重新上传。",
        { value0: reason, value1: path.replace(/^\/workspace\//, "./") },
      ),
    );
  }
  return path;
}
export function SessionFiles({
  sessionId,
  environmentId,
  writable,
  close,
  useFile,
}: {
  sessionId: string;
  environmentId: string;
  writable: boolean;
  close: () => void;
  useFile: (path: string) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const artifacts = useInfiniteQuery({
    queryKey: ["artifacts", sessionId],
    initialPageParam: "",
    queryFn: async ({
      pageParam,
      signal,
    }): Promise<ListPage<SessionArtifact>> => {
      return listSessionArtifacts(sessionId, { limit: 50, order: "desc", after: pageParam || undefined, signal });
    },
    getNextPageParam: (page) =>
      page.has_more ? page.last_id || undefined : undefined,
  });
  const send = useMutation({
    mutationFn: (file: File) => upload(environmentId, file),
    onSuccess: (path) => {
      useFile(path.replace(/^\/workspace\//, "./"));
      close();
    },
  });
  const download = useMutation({
    mutationFn: (file: SessionArtifact) => downloadArtifact(sessionId, file),
  });
  const files = artifacts.data?.pages.flatMap((page) => page.data) || [];
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !send.isPending) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{tr("会话文件")}</DialogTitle>
        </DialogHeader>
        <div className="flex items-center gap-3">
          <Button
            disabled={!writable || send.isPending}
            onClick={() => input.current?.click()}
          >
            <Upload />
            {send.isPending ? tr("上传中\u2026") : tr("上传文件")}
          </Button>
          <input
            ref={input}
            aria-label={tr("选择上传文件")}
            type="file"
            className="hidden"
            disabled={!writable || send.isPending}
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) send.mutate(file);
              event.target.value = "";
            }}
          />
        </div>
        {!writable && (
          <p role="status" className="text-sm text-fg-muted">
            {tr("会话空闲且运行环境就绪后可以上传。")}
          </p>
        )}
        <ErrorNotice
          error={send.error || download.error || artifacts.error}
          retry={
            !send.error && !download.error && artifacts.error
              ? () => void artifacts.refetch()
              : undefined
          }
        />
        <h3 className="text-base font-semibold">{tr("生成的文件")}</h3>
        <div className="max-h-80 space-y-3 overflow-auto">
          {artifacts.isPending ? (
            <p role="status">{tr("正在读取\u2026")}</p>
          ) : !files.length && !artifacts.error ? (
            <p className="text-sm text-fg-muted">
              {tr("还没有生成的文件。")}
            </p>
          ) : null}
          {files.map((file) => (
            <div
              key={file.id}
              className="flex items-center gap-3 rounded-xl border border-line px-4 py-3"
            >
              <div className="min-w-0 flex-1">
                <p className="break-all text-sm font-medium">
                  {file.path.replace(/^\/workspace\/outputs\//, "")}
                </p>
                <p className="text-sm text-fg-muted">
                  {Math.ceil(file.size_bytes / 1024)} KiB ·{" "}
                  {dateTime(file.created_at)}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon"
                disabled={download.isPending}
                onClick={() => download.mutate(file)}
                aria-label={tr("下载 {{value0}}", {
                  value0: file.path.split("/").at(-1),
                })}
              >
                <Download />
              </Button>
            </div>
          ))}
        </div>
        {artifacts.hasNextPage && (
          <Button
            variant="ghost"
            disabled={artifacts.isFetchingNextPage}
            onClick={() => void artifacts.fetchNextPage()}
          >
            {tr("加载更多")}
          </Button>
        )}
      </DialogContent>
    </Dialog>
  );
}
