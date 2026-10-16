import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { product } from "./lib/product";
import { tr } from "./lib/i18n";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { ErrorNotice, Field } from "./components/shared";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";

export function OAuthCredentialDialog({
  vaultId,
  onClose,
}: {
  vaultId: string;
  onClose(): void;
}) {
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [id] = useState(() => crypto.randomUUID());
  const begin = useMutation({
    mutationFn: () =>
      product<{ authorization_url: string }>(`mcp-oauth/${id}`, "POST", {
        action: "begin",
        vault_id: vaultId,
        name,
        url,
      }),
    onSuccess: (result) => location.assign(result.authorization_url),
  });
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{tr("OAuth 授权")}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            begin.mutate();
          }}
        >
          <Field id="oauth-name" label={tr("名称")}>
            <Input
              id="oauth-name"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field id="oauth-url" label={tr("HTTPS MCP 地址")}>
            <Input
              id="oauth-url"
              type="url"
              required
              value={url}
              onChange={(e) => setURL(e.target.value)}
            />
          </Field>
          <p className="text-sm text-fg-muted">
            {tr(
              "将在服务商页面授权。授权完成后凭据保存在当前凭据库，可用于 MCP 配置。",
            )}
          </p>
          <ErrorNotice error={begin.error} />
          <DialogFooter>
            <Button type="submit" disabled={begin.isPending}>
              {tr("前往授权")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function OAuthCallback() {
  const [params] = useState(() => new URLSearchParams(location.search));
  useEffect(() => {
    history.replaceState(null, "", "/oauth/callback");
  }, []);
  const [id, state] = (params.get("state") || "").split(":");
  const finish = useMutation({
    mutationFn: () =>
      product(`mcp-oauth/${id}`, "POST", {
        action: "complete",
        state,
        code: params.get("code"),
      }),
    onSuccess: () => location.replace("/#/credentials"),
  });
  const denied = params.has("error") || !id || !state || !params.get("code");
  return (
    <main className="mx-auto max-w-md space-y-4 p-8">
      <h1 className="text-lg font-medium">{tr("OAuth 授权")}</h1>
      <p>
        {denied
          ? tr("授权未完成，请返回凭据页面重试。")
          : tr("服务商授权已完成，点击保存凭据。")}
      </p>
      <ErrorNotice error={finish.error} />
      {!denied && (
        <Button disabled={finish.isPending} onClick={() => finish.mutate()}>
          {tr("保存凭据")}
        </Button>
      )}
      <a className="block underline" href="/#/credentials">
        {tr("返回凭据")}
      </a>
    </main>
  );
}
