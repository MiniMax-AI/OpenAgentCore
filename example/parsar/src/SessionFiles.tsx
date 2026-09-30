import { useRef } from "react";
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";
import { Upload, Download } from "lucide-react";
import type { ListPage, SessionArtifact } from "@oac/agents-client";
import { api, dateTime } from "./lib/api";
import { Button } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { ErrorNotice, Help } from "./components/shared";

const maxUploadBytes = 5 * 1024 * 1024;

async function upload(environmentId: string, file: File) {
  if (file.size > maxUploadBytes) throw new Error("文件不能超过 5 MiB。");
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
  const path = `/workspace/inputs/${crypto.randomUUID()}-${name}`;
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
    reader.onerror = () => reject(new Error("无法读取文件，请重新选择。"));
    reader.readAsDataURL(file);
  });
  try {
    await api.createEnvironmentFile(environmentId, {
      type: "inline",
      path,
      data,
    });
  } catch (error) {
    const reason = error instanceof Error ? error.message : "请求失败";
    throw new Error(
      `${reason} 上传未确认，文件可能已写入 ${path.replace(/^\/workspace\//, "./")}。请先检查该路径再重新上传。`,
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
      const params = new URLSearchParams({ limit: "50", order: "desc" });
      if (pageParam) params.set("after", pageParam);
      const response = await fetch(
        `/v1/agents/sessions/${encodeURIComponent(sessionId)}/artifacts?${params}`,
        { signal },
      );
      const value = await response.json();
      if (!response.ok)
        throw new Error(value.error?.message || "无法读取产物。");
      return value;
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
    mutationFn: async (file: SessionArtifact) => {
      const response = await fetch(
        `/v1/agents/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(file.id)}/content`,
      );
      if (!response.ok) {
        const value = await response.json();
        throw new Error(value.error?.message || "下载失败，请重试。");
      }
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = file.path.split("/").at(-1) || "artifact";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
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
          <DialogTitle>会话文件</DialogTitle>
        </DialogHeader>
        <div className="flex items-center gap-3">
          <Button
            disabled={!writable || send.isPending}
            onClick={() => input.current?.click()}
          >
            <Upload />
            {send.isPending ? "上传中…" : "上传文件"}
          </Button>
          <Help>
            每次上传一个文件，最大 5
            MiB。文件进入当前工作区，路径会插入消息草稿；发送后 Agent
            才会处理。执行期间不能上传。让 Agent 将结果保存到当前工作目录的
            outputs/，执行完成后可在这里下载。
          </Help>
          <input
            ref={input}
            aria-label="选择上传文件"
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
          <p role="status" className="text-base">
            会话空闲且运行环境就绪后可以上传。
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
        <h3 className="text-base font-medium">生成的文件</h3>
        <div className="max-h-80 space-y-3 overflow-auto">
          {artifacts.isPending ? (
            <p role="status">正在读取…</p>
          ) : !files.length && !artifacts.error ? (
            <p className="text-base text-fg-muted">还没有生成的文件。</p>
          ) : null}
          {files.map((file) => (
            <div
              key={file.id}
              className="flex items-center gap-3 rounded-lg border border-line p-3"
            >
              <div className="min-w-0 flex-1">
                <p className="break-all text-base">
                  {file.path.replace(/^\/workspace\/outputs\//, "")}
                </p>
                <p className="text-base text-fg-muted">
                  {Math.ceil(file.size_bytes / 1024)} KiB ·{" "}
                  {dateTime(file.created_at)}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon"
                disabled={download.isPending}
                onClick={() => download.mutate(file)}
                aria-label={`下载 ${file.path.split("/").at(-1)}`}
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
            加载更多
          </Button>
        )}
      </DialogContent>
    </Dialog>
  );
}
