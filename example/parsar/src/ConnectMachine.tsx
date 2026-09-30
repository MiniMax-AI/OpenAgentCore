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

const shell = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
const powershell = (value: string) => `'${value.replaceAll("'", "''")}'`;

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
  const [copied, setCopied] = useState(false);
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
  const command = windows
    ? `$env:OAC_RUNTIME_HOME = "$HOME\\.oac\\sessions\\${id}"\noac-daemon.exe install --remote ${powershell(environment.remote_url)} --environment-id ${powershell(id)} --workspace ${powershell(machine.workspace_directory)} --credential-file "$HOME\\executor-credential-${id}.json"\noac-daemon.exe start`
    : `export OAC_RUNTIME_HOME="$HOME/.oac/sessions/${id}"\nchmod 600 "$HOME/executor-credential-${id}.json"\noac-daemon install --remote ${shell(environment.remote_url)} --environment-id ${shell(id)} --workspace ${shell(machine.workspace_directory)} --credential-file "$HOME/executor-credential-${id}.json"\noac-daemon start`;
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
            <p>
              1. 在用户机器安装与 Core 同版本的 oac-daemon，以及此 Agent 使用的
              Codex 或 Claude Code。确认工作目录已经存在。
            </p>
            <div className="flex items-center gap-2">
              <span>安装前提</span>
              <Help>
                Linux、macOS 使用原生 daemon；Windows 使用
                oac-daemon.exe，Claude Code 还需要 Git Bash。daemon
                使用启动用户的权限，不提供文件或网络隔离。
              </Help>
            </div>
            <p>
              2. 在 Core 控制台的「Session
              log」打开此会话，签发执行凭据并下载到用户机器的主目录，命名为：
            </p>
            <code className="block break-all rounded-lg bg-surface-secondary p-3">
              executor-credential-{id}.json
            </code>
            <div className="flex items-center gap-3">
              <span>Core 会话</span>
              <code className="min-w-0 break-all">{session.id}</code>
              <Button
                variant="ghost"
                onClick={() => void navigator.clipboard.writeText(session.id)}
              >
                复制 ID
              </Button>
            </div>
            <p>3. 在{windows ? " PowerShell" : "终端"}执行：</p>
            <pre className="overflow-x-auto rounded-lg border border-line p-4 text-base">
              <code>{command}</code>
            </pre>
            <Button
              variant="outline"
              onClick={async () => {
                await navigator.clipboard.writeText(command);
                setCopied(true);
              }}
            >
              {copied ? "已复制" : "复制命令"}
            </Button>
            <p>显示「已连接」后，回到会话发送消息。</p>
            <Help>
              每个会话使用独立的 Runtime 主目录和连接凭据。已有安装只需使用相同
              OAC_RUNTIME_HOME 运行 start，不要重复 install。stop
              会保留机器文件和会话历史。
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
