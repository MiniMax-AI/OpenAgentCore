import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import { tr } from "./lib/i18n";
import { product } from "./lib/product";
import { Button } from "./components/ui/button";
import { Textarea } from "./components/ui/textarea";
import { ErrorNotice, Field } from "./components/shared";
export interface ConnectionLogin {
  id: string;
  status: string;
  verification_url?: string;
  user_code?: string;
  expires_at: number;
  error?: string;
}
export interface ConnectionEntry {
  service: string;
  label?: string;
  default_scopes?: string;
  bot_id: string;
  app_id: string;
  name: string;
  required_scopes?: string;
  connection?: {
    name: string;
    subject_id: string;
    status: string;
    revocation_pending?: boolean;
    scopes: string;
    expires_at: number;
  };
  login?: ConnectionLogin;
}
export const useConnections = () =>
  useQuery({
    queryKey: ["connections"],
    queryFn: () => product<ConnectionEntry[]>("connections"),
    refetchInterval: 2000,
  });
export function LoginCard({
  login,
  retry,
  pending,
}: {
  login: ConnectionLogin;
  retry: () => void;
  pending: boolean;
}) {
  const active = login.status === "pending" && login.expires_at > Date.now();
  return (
    <div className="space-y-4 rounded-lg border border-line p-5">
      <h2 className="font-medium">
        {tr(
          active
            ? "等待连接账号"
            : login.status === "connected"
              ? "账号已连接"
              : "需要重新连接账号",
        )}
      </h2>
      {active && login.verification_url ? (
        <>
          <p className="text-sm text-fg-muted">
            {tr(
              "打开链接或扫码，确认账号和权限。授权完成后任务会自动继续，无需重新发送。",
            )}
          </p>
          <a
            className="block break-all text-sm underline"
            href={login.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {login.verification_url}
          </a>
          <div className="inline-block rounded bg-white p-3">
            <QRCodeSVG value={login.verification_url} size={160} />
          </div>
          {login.user_code && (
            <p className="text-sm">
              {tr("验证码")}: <code>{login.user_code}</code>
            </p>
          )}
          <p className="text-xs text-fg-muted">
            {tr("链接有效期至")}:{" "}
            {new Date(login.expires_at).toLocaleTimeString()}
          </p>
        </>
      ) : (
        login.status !== "connected" && (
          <>
            <p role="alert" className="text-sm">
              {login.error || tr("授权已过期或被取消，原任务仍保留。")}
            </p>
            <Button disabled={pending} onClick={retry}>
              {tr("重新连接")}
            </Button>
          </>
        )
      )}
    </div>
  );
}
function ConnectionItem({ entry }: { entry: ConnectionEntry }) {
  const cache = useQueryClient();
  const [scopes, setScopes] = useState(
    entry.required_scopes || entry.default_scopes || "",
  );
  const action = useMutation({
    mutationFn: (method: string) =>
      product(`connections/${entry.service}/${entry.bot_id}`, method, {
        scopes,
      }),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["connections"] }),
  });
  const connected = entry.connection?.status === "connected";
  return (
    <section className="max-w-2xl space-y-4 rounded-lg border border-line p-5">
      <div>
        <h2 className="font-medium">
          {tr(entry.label || "账号连接")} · {entry.name}
        </h2>
        <p className="mt-1 text-xs text-fg-muted">{entry.app_id}</p>
      </div>
      {connected && (
        <div className="text-sm">
          <p>
            {tr("已连接")}: {entry.connection!.name}
          </p>
          <p className="text-fg-muted">{entry.connection!.subject_id}</p>
          <p className="mt-2 break-all">
            {tr("已授权范围")}: {entry.connection!.scopes}
          </p>
        </div>
      )}
      {entry.connection?.revocation_pending && (
        <p role="status" className="text-sm">
          {tr("已停止使用此连接，正在向服务提供方撤销授权。")}
        </p>
      )}
      <Field label={tr("授权范围")} id={`scopes-${entry.bot_id}`}>
        <Textarea
          id={`scopes-${entry.bot_id}`}
          value={scopes}
          onChange={(e) => setScopes(e.target.value)}
          rows={3}
        />
      </Field>
      <p className="text-xs text-fg-muted">
        {tr(
          "Agent 声明需要的权限；应用需先开通相应用户权限。个人授权不会保存到 Agent 或环境模板。",
        )}
      </p>
      <div className="flex gap-2">
        <Button
          disabled={action.isPending}
          onClick={() => action.mutate("POST")}
        >
          {tr(connected ? "更新授权" : "连接账号")}
        </Button>
        {connected && (
          <Button
            variant="outline"
            disabled={action.isPending}
            onClick={() => action.mutate("DELETE")}
          >
            {tr("断开连接")}
          </Button>
        )}
      </div>
      <ErrorNotice error={action.error} />
      {entry.login && entry.login.status !== "connected" && (
        <LoginCard
          login={entry.login}
          pending={action.isPending}
          retry={() => action.mutate("POST")}
        />
      )}
    </section>
  );
}
export function Connections() {
  const query = useConnections();
  return (
    <div className="h-full overflow-y-auto p-6">
      <h1 className="text-xl font-semibold">{tr("我的连接")}</h1>
      <p className="my-4 max-w-2xl text-sm text-fg-muted">
        {tr(
          "连接你自己的服务账号，供任务按需使用。新会话自动复用并续期；旧会话凭据过期后需要开启新会话。断开后停止新执行并向服务端撤销授权。",
        )}
      </p>
      <ErrorNotice error={query.error} />
      <div className="space-y-4">
        {query.data?.map((entry) => (
          <ConnectionItem key={entry.bot_id} entry={entry} />
        ))}
      </div>
      {query.data?.length === 0 && (
        <a href="#/feishu" className="underline">
          {tr("先配置飞书应用")}
        </a>
      )}
    </div>
  );
}
