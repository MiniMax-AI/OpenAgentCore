import {
  connectionFields,
  environmentVariableName,
} from "../shared/connection-bindings.mjs";
import { posix } from "node:path";
import { AppError, text } from "./store.mjs";

// Product-owned preparation. Core validates the public Environment request again.
export function runtimeProfile(body, previous) {
  if (body.environment !== undefined && body.environment !== "openai_hosted")
    throw new AppError(400, "Parsar 仅支持托管工作环境。");
  const packages = {};
  for (const manager of ["npm", "python"]) {
    const entries = body.packages?.[manager] ?? [];
    if (
      !Array.isArray(entries) ||
      entries.length > 100 ||
      entries.some(
        (v) =>
          typeof v !== "string" ||
          !v.trim() ||
          v.startsWith("-") ||
          /[\x00-\x20\x7f]/.test(v) ||
          v.length > 512,
      )
    )
      throw new AppError(400, "软件包格式无效，请每行填写一个包名和可选版本。");
    packages[manager] = [...new Set(entries)];
  }
  if (
    body.packages &&
    Object.keys(body.packages).some((k) => !["npm", "python"].includes(k))
  )
    throw new AppError(400, "环境模板支持 npm 和 Python 软件包。");
  const env = Object.create(null);
  const input = body.env === undefined ? previous?.env || {} : body.env;
  if (
    !input ||
    typeof input !== "object" ||
    Array.isArray(input) ||
    Object.keys(input).length > 100
  )
    throw new AppError(400, "环境变量格式无效。");
  for (const [name, value] of Object.entries(input)) {
    if (!environmentVariableName(name))
      throw new AppError(400, "环境变量名称无效或被运行环境保留。");
    const selected = value === null ? previous?.env?.[name] : value;
    if (
      typeof selected !== "string" ||
      selected.includes("\0") ||
      selected.length > 8192
    )
      throw new AppError(400, "请填写新环境变量的值；已有变量留空可保留。");
    env[name] = selected;
  }
  const connection_env = body.connection_env ?? {};
  if (
    !connection_env ||
    typeof connection_env !== "object" ||
    Array.isArray(connection_env) ||
    Object.keys(connection_env).length > 20
  )
    throw new AppError(400, "连接变量映射格式无效。");
  for (const [name, source] of Object.entries(connection_env)) {
    if (
      !environmentVariableName(name) ||
      Object.hasOwn(env, name) ||
      typeof source !== "string" ||
      !Object.hasOwn(connectionFields, source)
    )
      throw new AppError(
        400,
        "连接变量名称必须有效、不能与固定变量重复，且只能选择公开的连接字段。",
      );
  }
  const files = body.files ?? [];
  if (!Array.isArray(files) || files.length > 20)
    throw new AppError(400, "初始文件最多 20 个。");
  const paths = new Set();
  const normalized = files.map((file) => {
    const path = text(file.path, "文件路径", 1024, true);
    if (
      !path.startsWith("/workspace/") ||
      posix.normalize(path) !== path ||
      path.endsWith("/") ||
      /[\\\x00-\x1f\x7f]/.test(path) ||
      paths.has(path)
    )
      throw new AppError(400, "文件路径必须在 /workspace/ 内且不能重复。");
    paths.add(path);
    return { path, content: text(file.content, "文件内容", 100000) };
  });
  if (Buffer.byteLength(JSON.stringify(normalized)) > 500000)
    throw new AppError(400, "初始文件总大小不能超过 500 KB。");
  return {
    environment: "openai_hosted",
    description: text(body.description || "", "说明", 1000),
    packages,
    setup_script: text(body.setup_script || "", "初始化脚本", 100000),
    env,
    connection_env,
    files: normalized,
  };
}

export function publicRuntime({ env, ...row }) {
  return {
    ...row,
    env: Object.fromEntries(Object.keys(env || {}).map((key) => [key, null])),
  };
}

export function runtimeEnvironment(runtime) {
  if (runtime.environment !== "openai_hosted")
    throw new AppError(400, "Parsar 仅支持托管工作环境，请重新配置环境模板。");
  return {
    type: "openai_hosted",
    packages: runtime.packages || { npm: [], python: [] },
    env: runtime.env || {},
    setup_commands: runtime.setup_script
      ? [{ command: runtime.setup_script, cwd: "/workspace" }]
      : [],
    files: (runtime.files || []).map(({ path, content }) => ({
      type: "inline",
      path,
      data: Buffer.from(content).toString("base64"),
    })),
  };
}
