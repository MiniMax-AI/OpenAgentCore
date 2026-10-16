import { AppError, text, uuid } from "./store.mjs";
import { unzipSync } from "fflate";
const publicVersion = ({ archive, client_bundle, ...value }) => ({
  ...value,
  has_archive: !!archive,
  has_client_bundle: !!client_bundle,
});
export const publicCapability = (row) => ({
  ...row,
  versions: row.versions.map(publicVersion),
});
export function capabilityAPI(store) {
  return async (method, path, body) => {
    const match = path.match(/^\/app\/capabilities(?:\/([a-f0-9-]{36}))?$/);
    if (!match || (match[1] && !uuid.test(match[1])))
      throw new AppError(404, "Not found.");
    const id = match[1],
      old = id && store.get("capabilities", id);
    if (method === "GET") {
      if (id && !old) throw new AppError(404, "能力不存在。");
      return id
        ? publicCapability(old)
        : store.list("capabilities").map(publicCapability);
    }
    if (!id) throw new AppError(405, "Method not allowed.");
    if (old && old.revision !== body.revision)
      throw new AppError(409, "能力已更新，请重新加载。");
    if (method === "DELETE") {
      if (
        store.list("agents").some((agent) => agent.capability_ids?.includes(id))
      )
        throw new AppError(409, "能力仍被 Agent 引用。");
      store.remove("capabilities", id);
      return {};
    }
    if (method !== "PUT") throw new AppError(405, "Method not allowed.");
    const kind = old?.kind || body.kind;
    if (!["prompt", "knowledge", "plugin"].includes(kind))
      throw new AppError(400, "能力类型无效。");
    const name = text(body.name, "名称", 80, true);
    const description = text(body.description || "", "说明", 2000);
    const versions = [...(old?.versions || [])];
    if (
      !old ||
      body.content !== undefined ||
      body.archive ||
      body.client_bundle !== undefined
    ) {
      const version = {
        version: String(versions.length + 1),
        created_at: Date.now(),
      };
      if (kind === "plugin") {
        const archive = text(
          body.archive ||
            old?.versions.find((row) => row.version === old.default_version)
              ?.archive,
          "Plugin ZIP",
          44 * 1024 * 1024,
          true,
        );
        if (!/^[A-Za-z0-9+/]+={0,2}$/.test(archive))
          throw new AppError(400, "ZIP 编码无效。");
        try {
          let size = 0;
          const entries = unzipSync(Buffer.from(archive, "base64"), {
            filter: (file) => {
              size += file.originalSize;
              if (
                size > 32 * 1024 * 1024 ||
                file.name.split("/").includes("..") ||
                file.name.startsWith("/") ||
                file.name.includes("\\")
              )
                throw new Error();
              return true;
            },
          });
          if (
            !Object.keys(entries).some((path) =>
              /(?:^|\/)\.(?:codex|claude)-plugin\/plugin.json$/.test(path),
            )
          )
            throw new Error();
          version.files = Object.keys(entries);
          version.mcp_servers = Object.entries(entries)
            .filter(([path]) => /(?:^|\/)\.mcp.json$/.test(path))
            .flatMap(([, bytes]) =>
              Object.entries(
                JSON.parse(Buffer.from(bytes).toString()).mcpServers || {},
              ).map(([name, config]) => ({
                name,
                transport: config.command ? "stdio" : config.type || "http",
              })),
            );
        } catch {
          throw new AppError(
            400,
            "Plugin ZIP 无效，需要标准 Plugin manifest，解压后最多 32 MiB。",
          );
        }
        version.archive = archive;
        const client =
          body.client_bundle ??
          old?.versions.find((row) => row.version === old.default_version)
            ?.client_bundle;
        if (client !== undefined)
          version.client_bundle = text(
            client,
            "Client JavaScript",
            1024 * 1024,
          );
      } else version.content = text(body.content, "内容", 500000, true);
      versions.push(version);
    }
    const default_version = body.default_version || versions.at(-1).version;
    if (!versions.some((row) => row.version === default_version))
      throw new AppError(400, "版本不存在。");
    const value = {
      id,
      name,
      description,
      kind,
      versions,
      default_version,
      deprecated: body.deprecated === true,
      revision: (old?.revision || 0) + 1,
    };
    store.put("capabilities", value);
    return publicCapability(value);
  };
}
