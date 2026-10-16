import { tr } from "./lib/i18n";
import { OAuthCredentialDialog } from "./OAuthCredential";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, KeyRound, Trash2 } from "lucide-react";
import type { VaultCredential } from "@oac/agents-client";
import { api, readHistory } from "./lib/api";
import { PageHeader, ErrorNotice, Field } from "./components/shared";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
export const useVaults = () =>
  useQuery({
    queryKey: ["vaults"],
    queryFn: ({ signal }) =>
      readHistory((options) => api.listVaults(options), signal),
  });
export function Credentials() {
  const vaults = useVaults(),
    cache = useQueryClient(),
    [vaultId, setVaultId] = useState(""),
    [create, setCreate] = useState(false),
    [oauth, setOAuth] = useState(false),
    [editing, setEditing] = useState<VaultCredential | null>(null),
    [name, setName] = useState(""),
    [url, setURL] = useState(""),
    [token, setToken] = useState("");
  const selected = vaultId || vaults.data?.[0]?.id || "";
  const credentials = useQuery({
    queryKey: ["vault-credentials", selected],
    enabled: !!selected,
    queryFn: ({ signal }) =>
      readHistory(
        (options) => api.listVaultCredentials(selected, options),
        signal,
      ),
  });
  const addVault = useMutation({
    mutationFn: (name: string) => api.createVault({ name }),
    onSuccess: (vault) => {
      setVaultId(vault.id);
      void cache.invalidateQueries({ queryKey: ["vaults"] });
    },
  });
  const save = useMutation({
    mutationFn: () =>
      editing
        ? api.replaceVaultCredentialToken(selected, editing.id, {
            auth: { type: "static_bearer", token },
          })
        : api.createVaultCredential(selected, {
            name,
            auth: { type: "static_bearer", mcp_server_url: url, token },
          }),
    onSuccess: () => {
      setToken("");
      setCreate(false);
      setEditing(null);
      void cache.invalidateQueries({
        queryKey: ["vault-credentials", selected],
      });
    },
  });
  const remove = useMutation({
    mutationFn: (row: VaultCredential) =>
      api.deleteVaultCredential(selected, row.id),
    onSuccess: () =>
      cache.invalidateQueries({ queryKey: ["vault-credentials", selected] }),
  });
  return (
    <>
      <PageHeader title={tr("凭据")}>
        <Button
          variant="outline"
          disabled={!selected}
          onClick={() => setOAuth(true)}
        >
          {tr("OAuth 授权")}
        </Button>
        <Button
          variant="outline"
          onClick={() => {
            const name = prompt(tr("凭据库名称"));
            if (name?.trim()) addVault.mutate(name.trim());
          }}
        >
          {tr("新建凭据库")}
        </Button>
        <Button
          disabled={!selected}
          onClick={() => {
            setName("");
            setURL("");
            setToken("");
            setEditing(null);
            setCreate(true);
          }}
        >
          <Plus />
          {tr("添加凭据")}
        </Button>
      </PageHeader>
      <ErrorNotice
        error={
          vaults.error || credentials.error || addVault.error || remove.error
        }
      />
      <div className="flex h-10 shrink-0 items-center gap-3 border-b border-line px-6">
        <Select
          aria-label={tr("凭据库")}
          value={selected}
          onValueChange={setVaultId}
        >
          <SelectOption value="">{tr("选择凭据库")}</SelectOption>
          {vaults.data?.map((vault) => (
            <SelectOption key={vault.id} value={vault.id}>
              {vault.name || vault.id}
            </SelectOption>
          ))}
        </Select>
        <span className="text-xs text-fg-muted">
          {tr("密钥只写入 Core Vault，不返回浏览器。")}
        </span>
      </div>
      <Ledger columns="16px minmax(160px,1fr) minmax(220px,2fr) 130px">
        <LedgerHeader>
          <span />
          <span>{tr("名称")}</span>
          <span>{tr("MCP 地址")}</span>
          <span />
        </LedgerHeader>
        {credentials.data?.map((row) => (
          <LedgerRow key={row.id}>
            <KeyRound className="h-3.5 w-3.5 text-fg-muted" />
            <span>{row.name}</span>
            <span className="font-mono text-xs text-fg-muted">
              {row.auth.mcp_server_url}
            </span>
            <div className="flex justify-end">
              <Button
                variant="ghost"
                disabled={row.auth.type !== "static_bearer"}
                onClick={() => {
                  setEditing(row);
                  setToken("");
                  setCreate(true);
                }}
              >
                {tr("更换密钥")}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("删除凭据 {{value0}}", { value0: row.name })}
                onClick={() => {
                  if (
                    confirm(tr("删除凭据 {{value0}}？", { value0: row.name }))
                  )
                    remove.mutate(row);
                }}
              >
                <Trash2 />
              </Button>
            </div>
          </LedgerRow>
        ))}
      </Ledger>
      {oauth && (
        <OAuthCredentialDialog
          vaultId={selected}
          onClose={() => setOAuth(false)}
        />
      )}
      {create && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open && !save.isPending) {
              setCreate(false);
              setToken("");
            }
          }}
        >
          <DialogContent aria-describedby={undefined}>
            <DialogHeader>
              <DialogTitle>
                {editing ? tr("更换密钥") : tr("添加 MCP 凭据")}
              </DialogTitle>
            </DialogHeader>
            <form
              id="credential-form"
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate();
              }}
            >
              {!editing && (
                <>
                  <Field label={tr("名称")} id="credential-name">
                    <Input
                      id="credential-name"
                      required
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                  </Field>
                  <Field label={tr("HTTPS MCP 地址")} id="credential-url">
                    <Input
                      id="credential-url"
                      type="url"
                      required
                      value={url}
                      onChange={(e) => setURL(e.target.value)}
                    />
                  </Field>
                </>
              )}
              <Field label="Bearer Token" id="credential-token">
                <Input
                  id="credential-token"
                  type="password"
                  autoComplete="new-password"
                  required
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                />
              </Field>
              <ErrorNotice error={save.error} />
            </form>
            <DialogFooter>
              <Button
                type="submit"
                form="credential-form"
                disabled={save.isPending}
              >
                {tr("保存凭据")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}
