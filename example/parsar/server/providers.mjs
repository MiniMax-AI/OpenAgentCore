import { randomUUID } from "node:crypto";
import { AppError, text } from "./store.mjs";
import { orcaRouterAPI } from "./orcarouter.mjs";
import { orcaInferenceBase, orcaOrigins } from "../shared/orcarouter.mjs";

export function publicProvider({ api_key, ...provider }) {
  return { ...provider, has_api_key: Boolean(api_key) };
}

function connection(body, previous) {
  const base = text(
    body.base_url ?? previous?.base_url ?? "",
    "Base URL",
    2000,
  ).trim();
  let base_url = "";
  if (base) {
    let url;
    try {
      url = new URL(base);
    } catch {
      throw new AppError(400, "Base URL 无效。");
    }
    if (
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      !(
        url.protocol === "https:" ||
        (url.protocol === "http:" &&
          ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname))
      )
    )
      throw new AppError(
        400,
        "Base URL 使用 HTTPS，或本机 HTTP 地址，不包含凭据和查询参数。",
      );
    base_url = url.href.replace(/\/+$/, "");
  }
  const api_key = text(
    body.api_key ?? previous?.api_key ?? "",
    "API Key",
    8192,
  ).trim();
  if (/[\r\n]/.test(api_key)) throw new AppError(400, "API Key 无效。");
  return { base_url, api_key };
}

export function providerAPI(store, fetchImpl) {
  const orcaAccounts = orcaRouterAPI(store, fetchImpl);
  return {
    async discover(body) {
      const { base_url, api_key } = connection(
        body,
        body.id ? store.get("providers", body.id) : undefined,
      );
      // An OrcaRouter account reads its own catalog through the credential
      // adapter, which owns the capability filter and the degraded ladder.
      const existing = body.id ? store.get("providers", body.id) : undefined;
      const mode = body.capability ?? "chat";
      if (existing?.provider === "orcarouter")
        return orcaAccounts.catalog(body.id, mode);
      if (!base_url) throw new AppError(400, "请填写 Base URL。");
      const endpoint = base_url.endsWith("/v1")
        ? `${base_url}/models`
        : `${base_url}/v1/models`;
      try {
        const response = await fetchImpl(endpoint, {
          method: "GET",
          redirect: "manual",
          signal: AbortSignal.timeout(15000),
          headers: {
            Accept: "application/json",
            ...(api_key ? { Authorization: `Bearer ${api_key}` } : {}),
          },
        });
        if (!response.ok)
          throw new AppError(
            502,
            `获取模型失败（HTTP ${response.status}），请检查地址和密钥。`,
          );
        const value = await response.json();
        if (!Array.isArray(value.data) || value.has_more === true)
          throw new AppError(
            502,
            "模型接口未返回完整的 data 列表，可手动添加模型。",
          );
        const models = [
          ...new Set(
            value.data
              .map((row) => row?.id)
              .filter(
                (id) => typeof id === "string" && id.trim() && id.length <= 200,
              ),
          ),
        ];
        return { models };
      } catch (error) {
        if (error instanceof AppError) throw error;
        throw new AppError(
          502,
          "无法获取模型，请检查地址和密钥，或手动添加模型。",
        );
      }
    },
    save(id, body, previous) {
      const value = {
        id,
        name: text(body.name, "名称", 80, true),
        revision: (previous?.revision || 0) + 1,
        ...connection(body, previous),
      };
      // OrcaRouter is a named provider preset: its inference base is the
      // gateway's own, so the operator never types a URL that could silently
      // point somewhere else. Any other provider keeps manual entry.
      if (body.preset === "orcarouter" || previous?.provider === "orcarouter") {
        value.provider = "orcarouter";
        value.base_url = orcaInferenceBase(orcaOrigins(process.env).api);
        // The API Key and the PKCE connect flow both write `api_key`; the
        // credential adapter that produced it stays recorded for the two
        // distinct status entries in the UI.
        value.credential_source =
          body.credential_source === "pkce"
            ? "pkce"
            : body.api_key || !previous?.api_key
              ? "api_key"
              : previous?.credential_source || "api_key";
        value.status = "ok";
        value.generation = (previous?.generation ?? 0) + (body.api_key ? 1 : 0);
        if (typeof body.account === "string" && body.account.trim())
          value.account = body.account.trim().slice(0, 200);
      }
      const existing = store
        .list("models")
        .filter((model) => model.provider_id === id);
      let selected;
      if (body.models !== undefined) {
        if (!Array.isArray(body.models) || body.models.length > 1000)
          throw new AppError(400, "最多选择 1000 个模型。");
        selected = body.models.map((row) => ({
          model: text(row?.model, "模型 ID", 200, true).trim(),
          name: text(row?.name, "模型名称", 80, true).trim(),
        }));
        if (new Set(selected.map((row) => row.model)).size !== selected.length)
          throw new AppError(400, "模型 ID 不能重复。");
      }
      return store.transaction(() => {
        if (selected) {
          const removed = existing.filter(
            (row) => !selected.some((entry) => entry.model === row.model),
          );
          if (
            store
              .list("agents")
              .some((agent) => removed.some((row) => row.id === agent.model_id))
          )
            throw new AppError(
              409,
              "取消的模型仍被 Agent 引用，请先调整 Agent 的模型。",
            );
          for (const row of removed) store.remove("models", row.id);
          for (const row of selected) {
            const old = existing.find((entry) => entry.model === row.model);
            store.put("models", {
              ...row,
              id: old?.id || randomUUID(),
              provider_id: id,
              revision: (old?.revision || 0) + 1,
            });
          }
        }
        store.put("providers", value);
        return publicProvider(value);
      });
    },
    /** The OrcaRouter credential seam: one interface, two adapters. */
    orca: orcaAccounts,
  };
}
