export interface LocalDockerGuideProfile {
  image: string;
  apiContainer: string;
  user: string;
  credentialsHomePath: string;
  runtimeHomePath: string;
}

export interface LocalDockerBackendGuideProfile {
  databaseContainer: string;
  apiContainer: string;
  daemonContainer: string;
  corePort: number;
}

const dockerImagePattern = /^[A-Za-z0-9][A-Za-z0-9._/:@-]*$/;
const dockerContainerPattern = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/;
const dockerUserPattern = /^[1-9][0-9]*:[1-9][0-9]*$/;
const homePathSegmentPattern = /^[A-Za-z0-9._-]+$/;

function required(
  env: Record<string, string>,
  name: string,
  feature = "OAC_WEB_DOCKER_GUIDE",
): string {
  const value = env[name];
  if (!value) throw new Error(`${name} is required when ${feature}=1.`);
  return value;
}

function validHomeRelativePath(value: string): boolean {
  if (value.startsWith("/") || value.endsWith("/") || value.includes("//")) return false;
  const segments = value.split("/");
  return segments.length > 0 && segments.every((segment) => (
    segment !== "." && segment !== ".." && homePathSegmentPattern.test(segment)
  ));
}

export function loadLocalDockerGuideProfile(
  env: Record<string, string>,
): LocalDockerGuideProfile | null {
  if (env.OAC_WEB_DOCKER_GUIDE !== "1") return null;

  const profile: LocalDockerGuideProfile = {
    image: required(env, "OAC_WEB_DOCKER_IMAGE"),
    apiContainer: required(env, "OAC_WEB_DOCKER_API_CONTAINER"),
    user: required(env, "OAC_WEB_DOCKER_USER"),
    credentialsHomePath: required(env, "OAC_WEB_DOCKER_CREDENTIALS_HOME_PATH"),
    runtimeHomePath: required(env, "OAC_WEB_DOCKER_RUNTIME_HOME_PATH"),
  };

  if (!dockerImagePattern.test(profile.image)) {
    throw new Error("OAC_WEB_DOCKER_IMAGE is not a safe Docker image reference.");
  }
  if (!dockerContainerPattern.test(profile.apiContainer)) {
    throw new Error("OAC_WEB_DOCKER_API_CONTAINER is not a safe Docker container name.");
  }
  if (!dockerUserPattern.test(profile.user)) {
    throw new Error("OAC_WEB_DOCKER_USER must be a numeric non-root uid:gid pair.");
  }
  if (!validHomeRelativePath(profile.credentialsHomePath)) {
    throw new Error("OAC_WEB_DOCKER_CREDENTIALS_HOME_PATH must be a safe HOME-relative path.");
  }
  if (!validHomeRelativePath(profile.runtimeHomePath)) {
    throw new Error("OAC_WEB_DOCKER_RUNTIME_HOME_PATH must be a safe HOME-relative path.");
  }

  return profile;
}

function validPort(value: string): number | null {
  if (!/^[1-9][0-9]{0,4}$/.test(value)) return null;
  const port = Number(value);
  return port <= 65_535 ? port : null;
}

export function loadLocalDockerBackendGuideProfile(
  env: Record<string, string>,
): LocalDockerBackendGuideProfile | null {
  if (env.OAC_WEB_DOCKER_BACKEND_GUIDE !== "1") return null;

  const feature = "OAC_WEB_DOCKER_BACKEND_GUIDE";
  const databaseContainer = required(env, "OAC_WEB_DOCKER_DATABASE_CONTAINER", feature);
  const apiContainer = required(env, "OAC_WEB_DOCKER_API_CONTAINER", feature);
  const daemonContainer = required(env, "OAC_WEB_DOCKER_DAEMON_CONTAINER", feature);
  const corePortValue = required(env, "OAC_WEB_DOCKER_CORE_PORT", feature);
  const corePort = validPort(corePortValue);

  for (const [name, value] of [
    ["OAC_WEB_DOCKER_DATABASE_CONTAINER", databaseContainer],
    ["OAC_WEB_DOCKER_API_CONTAINER", apiContainer],
    ["OAC_WEB_DOCKER_DAEMON_CONTAINER", daemonContainer],
  ] as const) {
    if (!dockerContainerPattern.test(value)) {
      throw new Error(`${name} is not a safe Docker container name.`);
    }
  }
  if (corePort === null) {
    throw new Error("OAC_WEB_DOCKER_CORE_PORT must be a valid TCP port.");
  }

  return { databaseContainer, apiContainer, daemonContainer, corePort };
}
