import { readFileSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";

export const DEFAULT_PROXY_TOKEN_FILE = "~/.oac/dev/web-token";

export interface ProxyBearerAuth {
  token: string;
  source: "environment" | "file";
  filePath?: string;
}

interface LoadProxyBearerAuthOptions {
  token?: string;
  tokenFile?: string;
  homeDir?: string;
  rootDir?: string;
}

function normalizeToken(value: string, source: string): string {
  const token = value.trim();
  if (!token) throw new Error(`${source} is empty.`);
  if (/\s/.test(token)) throw new Error(`${source} must contain one bearer token without whitespace.`);
  return token;
}

export function resolveProxyTokenFile(
  configuredPath = DEFAULT_PROXY_TOKEN_FILE,
  homeDir = homedir(),
  rootDir = process.cwd(),
): string {
  if (configuredPath === "~") return homeDir;
  if (configuredPath.startsWith("~/")) return join(homeDir, configuredPath.slice(2));
  return isAbsolute(configuredPath) ? configuredPath : resolve(rootDir, configuredPath);
}

export function loadProxyBearerAuth({
  token,
  tokenFile,
  homeDir = homedir(),
  rootDir = process.cwd(),
}: LoadProxyBearerAuthOptions = {}): ProxyBearerAuth | undefined {
  const configuredToken = token?.trim();
  const configuredFile = tokenFile?.trim();

  if (configuredToken && configuredFile) {
    throw new Error("Set only one of OAC_WEB_DEV_PROXY_TOKEN or OAC_WEB_DEV_PROXY_TOKEN_FILE.");
  }
  if (configuredToken) {
    return { token: normalizeToken(configuredToken, "OAC_WEB_DEV_PROXY_TOKEN"), source: "environment" };
  }

  const explicitFile = Boolean(configuredFile);
  const filePath = resolveProxyTokenFile(configuredFile || DEFAULT_PROXY_TOKEN_FILE, homeDir, rootDir);
  let metadata: ReturnType<typeof statSync>;
  try {
    metadata = statSync(filePath);
  } catch (error) {
    if (!explicitFile && (error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw new Error(`Cannot read OpenAgentCore bearer token file: ${filePath}`);
  }

  if (!metadata.isFile()) throw new Error(`OpenAgentCore bearer token path is not a file: ${filePath}`);
  if (process.platform !== "win32" && (metadata.mode & 0o077) !== 0) {
    throw new Error(`OpenAgentCore bearer token file must not be group/world accessible: ${filePath}`);
  }

  let contents: string;
  try {
    contents = readFileSync(filePath, "utf8");
  } catch {
    throw new Error(`Cannot read OpenAgentCore bearer token file: ${filePath}`);
  }

  return {
    token: normalizeToken(contents, `OpenAgentCore bearer token file ${filePath}`),
    source: "file",
    filePath,
  };
}
