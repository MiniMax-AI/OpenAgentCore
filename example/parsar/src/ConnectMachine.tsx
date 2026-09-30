import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type {
  AgentSession,
  SelfHostedAgentEnvironment,
} from "@oac/agents-client";
import type { SessionRecord } from "./lib/product";
import { api } from "./lib/api";
import { Button } from "./components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { ErrorNotice, Help } from "./components/shared";

export function ConnectMachine({
  session,
  machine,
  onConnected,
}: {
  session: AgentSession;
  machine: NonNullable<SessionRecord["self_hosted"]>;
  onConnected: (connected: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState("");
  const environment = session.environment as SelfHostedAgentEnvironment;
  const id = environment.type === "self_hosted" ? environment.id : "";
  const query = useQuery({
    queryKey: ["environment", id],
    queryFn: ({ signal }) => api.retrieveEnvironment(id, { signal }),
    enabled: Boolean(id),
    refetchInterval: 2000,
  });
  useEffect(() => {
    onConnected(!query.error && query.data?.status === "connected");
  }, [query.data?.status, query.error, onConnected]);
  if (environment.type !== "self_hosted") return null;
  const labels = {
    pending: "等待连接",
    connected: "已连接",
    disconnected: "已断开",
    expired: "已过期",
    failed: "连接失败",
  };
  const status = query.data?.status;
  const windows = machine.platform === "windows";
  const installation = session.x_agents_core?.installation;
  const command =
    installation?.status === "available" &&
    (installation.expires_at ?? 0) > Date.now() / 1000
      ? installation.commands?.[windows ? "powershell" : "posix"]
      : undefined;
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-6 py-3 text-base">
        <span>
          {query.error
            ? "连接状态读取失败"
            : status
              ? labels[status]
              : "读取连接状态…"}
        </span>
        <Button variant="outline" onClick={() => setOpen(true)}>
          连接用户机器
        </Button>
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="max-h-[90dvh] max-w-2xl overflow-y-auto"
          aria-describedby={undefined}
        >
          <DialogHeader>
            <DialogTitle>连接用户机器</DialogTitle>
          </DialogHeader>
          <div className="space-y-5 text-base">
            <p>在用户机器的{windows ? " PowerShell" : "终端"}中运行安装命令。</p>
            <div className="flex items-center gap-2">
              <span>工作目录：{machine.workspace_directory}</span>
              <Help>
                安装器会下载与 Core 匹配的 Runtime 和所选执行引擎，创建工作目录并连接此会话。
                Windows 上 Claude Code 还需要 Git Bash。Runtime 使用启动用户的权限。
              </Help>
            </div>
            {command ? (
              <>
                <pre className="overflow-x-auto rounded-lg border border-line p-4 text-base">
                  <code>{command}</code>
                </pre>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    onClick={async () => {
                      await navigator.clipboard.writeText(command);
                      setCopied(command);
                    }}
                  >
                    {copied === command ? "已复制" : "复制命令"}
                  </Button>
                  <Help>命令包含临时连接授权，请勿分享。过期后页面会自动获取新命令。</Help>
                </div>
              </>
            ) : (
              <p role="status">安装命令暂不可用，请检查 Core 的原生安装包配置，或稍后重试。</p>
            )}
            <p>安装完成并显示「已连接」后，即可发送消息。</p>
            <Help>
              每个会话使用独立的 Runtime 安装目录。续聊使用原会话和工作目录；
              重新连接时使用原安装目录，保留会话历史和文件。
            </Help>
            <ErrorNotice
              error={query.error}
              retry={() => void query.refetch()}
            />
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
