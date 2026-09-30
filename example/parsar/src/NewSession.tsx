import { sessionRestriction } from "../shared/session-profile.mjs";
import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  product,
  ProductError,
  useRuntimes,
  type AgentProfile,
  type SessionRecord,
} from "./lib/product";
import { beginTiming, requestReturned, timingNow } from "./lib/session-timing";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Select, SelectOption } from "./components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { ErrorNotice, Field, Help } from "./components/shared";
export function NewSession({
  agent,
  close,
}: {
  agent: AgentProfile;
  close: () => void;
}) {
  const cache = useQueryClient();
  const runtimes = useRuntimes();
  const [form, setForm] = useState(() => ({
    id: crypto.randomUUID(),
    name: "",
    input: "",
    agent_id: agent.id,
    runtime_id: "",
  }));
  const runtime = runtimes.data?.find(
    (runtime) => runtime.id === form.runtime_id,
  );
  const selfHosted = runtime?.environment === "self_hosted";
  const restriction = sessionRestriction(agent, runtime);
  const startedAt = useRef<number>(0);
  const [uncertain, setUncertain] = useState(false);
  const save = useMutation({
    onError: (error) =>
      setUncertain(
        !(
          error instanceof ProductError &&
          [400, 401, 403, 404, 413, 422].includes(error.status)
        ),
      ),
    mutationFn: () => {
      startedAt.current = timingNow();
      return product<SessionRecord>(`sessions/${form.id}`, "PUT", form);
    },
    onSettled: () => {
      void cache.invalidateQueries({ queryKey: ["sessions"] });
    },
    onSuccess: (session) => {
      if (session.core_session_id && !session.self_hosted) {
        beginTiming(
          session.core_session_id,
          form.id,
          undefined,
          startedAt.current,
        );
        requestReturned(session.core_session_id, form.id);
      }
      close();
      location.hash = `/sessions/${session.id}`;
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>开始会话</DialogTitle>
        </DialogHeader>
        <form
          id="session-form"
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="会话名称" id="session-name">
            <Input
              disabled={save.isPending || uncertain}
              id="session-name"
              required
              maxLength={80}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label="运行时" id="session-runtime">
            <Select
              disabled={save.isPending || uncertain}
              id="session-runtime"
              value={form.runtime_id}
              onValueChange={(runtime_id) => setForm({ ...form, runtime_id })}
            >
              <SelectOption value="">选择运行时</SelectOption>
              {runtimes.data?.map((runtime) => (
                <SelectOption key={runtime.id} value={runtime.id}>
                  {runtime.name}
                </SelectOption>
              ))}
            </Select>
            {!runtimes.isPending && !runtimes.data?.length && (
              <a className="underline" href="#/runtimes" onClick={close}>
                前往添加运行时
              </a>
            )}
          </Field>
          {!selfHosted && (
            <Field label="第一条消息" id="session-input">
              <Textarea
                disabled={save.isPending || uncertain}
                id="session-input"
                required
                rows={5}
                value={form.input}
                onChange={(e) => setForm({ ...form, input: e.target.value })}
                placeholder="希望 Agent 做什么？"
              />
            </Field>
          )}
          {restriction && (
            <p role="alert" className="text-base">
              {restriction}
            </p>
          )}
          {!restriction &&
            runtime &&
            (runtime.environment === "none" || agent.harness === "mcode") && (
              <p className="text-base">
                此组合使用 Core 部署的模型连接；这里选择的 Provider
                仅用于模型分组。
              </p>
            )}
          <ErrorNotice error={save.error || runtimes.error} />
          {uncertain && (
            <p>如请求结果未确定，可关闭弹窗，在会话列表中恢复同一次创建。</p>
          )}
        </form>
        <DialogFooter>
          <Help>
            用户机器需要先创建会话，再按连接指引启动
            daemon，连接后发送消息。此会话使用当前 Agent
            配置的副本。托管会话有独立工作区；继续同一会话可保留历史和仍在运行的工作区。纯文本运行时不提供工作区。
          </Help>
          <Button
            form="session-form"
            type="submit"
            disabled={
              Boolean(restriction) ||
              uncertain ||
              save.isPending ||
              !form.runtime_id ||
              !form.name.trim() ||
              (!selfHosted && !form.input.trim())
            }
          >
            {save.isPending ? "启动中…" : "开始"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
