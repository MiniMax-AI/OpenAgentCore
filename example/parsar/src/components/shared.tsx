import type { ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { AlertCircle } from "lucide-react";
import { CircleHelp } from "lucide-react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { errorText } from "../lib/api";
import { product, type OrcaRouterCatalog } from "../lib/product";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export function PageHeader({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <header className="flex min-h-16 shrink-0 flex-wrap items-center justify-between gap-3 border-b border-line px-6 py-3">
      <h1 className="min-w-0 truncate text-xl font-semibold tracking-tight">
        {title}
      </h1>
      <div className="flex items-center gap-2">{children}</div>
    </header>
  );
}

export function Help({
  children,
  label = "了解更多",
}: {
  children: ReactNode;
  label?: string;
}) {
  return (
    <Tooltip.Root delayDuration={150}>
      <Tooltip.Trigger asChild>
        <button
          type="button"
          aria-label={label}
          className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-fg-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-accent"
        >
          <CircleHelp className="h-4 w-4" />
        </button>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          sideOffset={6}
          className="app-shadow-floating z-[60] max-w-xs rounded-md border border-line bg-surface px-3 py-2 text-base text-fg"
        >
          {children}
          <Tooltip.Arrow className="fill-surface" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  if (!error) return null;
  return (
    <div
      role="alert"
      className="flex items-start gap-2 border-b border-line px-6 py-3 text-sm"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
      <div className="min-w-0 break-words">
        <p>{errorText(error)}</p>
        {retry && (
          <button className="mt-1 underline underline-offset-4" onClick={retry}>
            重新加载
          </button>
        )}
      </div>
    </div>
  );
}

export function Field({
  label,
  id,
  children,
}: {
  label: string;
  id: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-base text-fg-muted">
        {label}
      </label>
      {children}
    </div>
  );
}

/**
 * The two OrcaRouter choices, side by side and independently usable:
 * **API Key** pastes an existing `sk-orca-...` key; **Connect with OrcaRouter**
 * runs OAuth 2.0 + PKCE and issues a key the operator owns. Both end at the same
 * credential seam, and neither replaces the other.
 *
 * `change` is a monotonically increasing generation owned by the caller. Every
 * asynchronous result must still belong to the current one, so a late URL or a
 * late success from an earlier attempt can never be shown under the current
 * provider or overwrite a newer credential.
 *
 * `pagehide` invalidates the generation, clears the busy flag and the
 * authorization hint synchronously, then cancels the server work with
 * `keepalive` — the guarded `finally` of the invalidated request may correctly
 * refuse to mutate state and would otherwise leave a restored page busy.
 */
export function OrcaRouterConnect({
  id,
  connect,
  cancel,
  complete,
  disabled = false,
  change,
  onCredentials,
  onError,
}: {
  id: string;
  connect: () => Promise<{ authorize_url?: string; redirect_uri?: string } | null>;
  cancel: () => Promise<unknown>;
  complete: (code: string) => Promise<unknown>;
  disabled?: boolean;
  change: unknown;
  onCredentials: (catalog: OrcaRouterCatalog) => void;
  onError: (error: unknown) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [hint, setHint] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const generation = useRef(0);
  const active = useRef(false);

  // A change of provider or of authentication method releases the login lock.
  useEffect(() => {
    if (!active.current) return;
    active.current = false;
    setBusy(false);
    setHint(null);
    void cancel().catch(() => {});
  }, [change, cancel]);

  useEffect(() => {
    const leave = () => {
      if (!active.current) return;
      // Invalidate first, then clear the synchronous UI state, then ask the
      // server to cancel without waiting for an answer.
      generation.current += 1;
      active.current = false;
      setBusy(false);
      setHint(null);
      void cancel().catch(() => {});
    };
    window.addEventListener("pagehide", leave);
    return () => {
      window.removeEventListener("pagehide", leave);
      // A real unmount cancels the server work without writing UI state.
      if (active.current) {
        active.current = false;
        generation.current += 1;
        void cancel().catch(() => {});
      }
    };
  }, [cancel]);

  async function start() {
    if (disabled || active.current) return;
    generation.current += 1;
    const mine = generation.current;
    active.current = true;
    setBusy(true);
    setHint(null);
    setNotice(null);
    setCode("");
    try {
      const started = await connect();
      if (generation.current !== mine) return;
      setHint(started?.authorize_url ?? null);
    } catch (error) {
      if (generation.current !== mine) return;
      active.current = false;
      setBusy(false);
      onError(error);
    } finally {
      if (generation.current === mine) setBusy(false);
    }
  }

  async function submit() {
    if (!code.trim()) return;
    generation.current += 1;
    const mine = generation.current;
    try {
      await complete(code.trim());
      if (generation.current !== mine) return;
      setCode("");
      setHint(null);
      setNotice("已使用 OrcaRouter 账户连接。");
      onCredentials(await product<OrcaRouterCatalog>(`providers/${id}/orcarouter/catalog`, "POST", { capability: "chat" }));
    } catch (error) {
      if (generation.current !== mine) return;
      onError(error);
    }
  }

  return (
    <div className="space-y-3 rounded-lg border border-line p-3" data-orcarouter-auth-methods="">
      <div className="space-y-2">
        <p className="text-base font-medium">使用 OrcaRouter 账户连接（OAuth 2.0 + PKCE）</p>
        <p className="text-sm text-fg-muted">
          在浏览器中授权后，OrcaRouter 会向本机回调返回一次性授权码，本程序用 PKCE
          verifier 换取属于你的 API Key。没有浏览器或已有密钥时，可以直接在上方填写 API Key。
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={disabled || busy}
            onClick={() => void start()}
            data-orcarouter-connect=""
          >
            {busy ? "等待授权…" : "连接 OrcaRouter"}
          </Button>
          {busy && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                generation.current += 1;
                active.current = false;
                setBusy(false);
                setHint(null);
                void cancel().catch(() => {});
              }}
            >
              取消
            </Button>
          )}
        </div>
        {hint && (
          <p className="text-sm" data-orcarouter-hint="">
            浏览器未自动打开时，请手动访问：
            <a className="ml-1 underline underline-offset-4" href={hint} target="_blank" rel="noreferrer">
              {hint}
            </a>
          </p>
        )}
        {hint && (
          <div className="space-y-2">
            <Field label="授权码（浏览器未回调时粘贴）" id={`${id}-code`}>
              <Input
                id={`${id}-code`}
                value={code}
                autoComplete="off"
                onChange={(event) => setCode(event.target.value)}
              />
            </Field>
            <Button type="button" variant="outline" disabled={!code.trim()} onClick={() => void submit()}>
              完成连接
            </Button>
          </div>
        )}
        {notice && (
          <p className="text-sm text-fg-muted" role="status">
            {notice}
          </p>
        )}
      </div>
    </div>
  );
}
