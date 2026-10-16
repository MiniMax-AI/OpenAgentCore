import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { product } from "./lib/product";
import { tr } from "./lib/i18n";
import { dateTime } from "./lib/api";
import { Button } from "./components/ui/button";
import { ErrorNotice } from "./components/shared";
export function AgentMCP({ id }: { id: string }) {
  const cache = useQueryClient();
  const [token, setToken] = useState("");
  const path = `agents/${id}/mcp-token`;
  const status = useQuery({
    queryKey: ["mcp-token", id],
    queryFn: () => product<{ credential: { expires_at: number } | null }>(path),
  });
  const save = useMutation({
    mutationFn: (method: "POST" | "DELETE") =>
      product<{ token?: string }>(path, method, {}),
    onSuccess: (result) => {
      setToken(result.token || "");
      void cache.invalidateQueries({ queryKey: ["mcp-token", id] });
    },
  });
  const configuration = {
    mcpServers: {
      parsar: {
        type: "http",
        url: `${location.origin}/mcp/agents/${id}`,
        headers: { Authorization: `Bearer ${token || "<token>"}` },
      },
    },
  };
  return (
    <section className="space-y-4 overflow-auto p-4">
      <h2 className="font-medium">{tr("通过 MCP 调用 Agent")}</h2>
      <p className="text-sm text-fg-muted">
        {tr(
          "提供 ask_agent 和 get_run。使用 Agent 的默认运行时，业务提问在 Parsar 中回答。地址仅对本机客户端开放。",
        )}
      </p>
      <ErrorNotice error={status.error || save.error} />
      {status.data?.credential && (
        <p className="text-xs">
          {tr("到期时间")}: {dateTime(status.data.credential.expires_at / 1000)}
        </p>
      )}
      <div className="flex gap-2">
        <Button
          disabled={save.isPending}
          onClick={() => {
            if (
              !status.data?.credential ||
              confirm(tr("替换后旧密钥立即失效，继续？"))
            )
              save.mutate("POST");
          }}
        >
          {status.data?.credential ? tr("替换密钥") : tr("创建密钥")}
        </Button>
        {status.data?.credential && (
          <Button
            variant="outline"
            disabled={save.isPending}
            onClick={() => {
              if (confirm(tr("撤销 MCP 访问？"))) save.mutate("DELETE");
            }}
          >
            {tr("撤销")}
          </Button>
        )}
      </div>
      {token && (
        <p className="text-sm">{tr("密钥仅显示一次，请复制到客户端配置。")}</p>
      )}
      <pre className="overflow-auto rounded border border-line p-3 text-xs">
        {JSON.stringify(configuration, null, 2)}
      </pre>
      <Button
        variant="outline"
        disabled={!token}
        onClick={() =>
          navigator.clipboard.writeText(JSON.stringify(configuration, null, 2))
        }
      >
        {tr("复制配置")}
      </Button>
    </section>
  );
}
