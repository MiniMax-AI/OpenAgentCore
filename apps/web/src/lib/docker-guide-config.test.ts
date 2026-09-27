import { describe, expect, it } from "vitest";

import {
  loadLocalDockerBackendGuideProfile,
  loadLocalDockerGuideProfile,
} from "./docker-guide-config";

const valid = {
  OAC_WEB_DOCKER_GUIDE: "1",
  OAC_WEB_DOCKER_IMAGE: "agents-core-web-executor:2b34ea46-codex-0.153.4",
  OAC_WEB_DOCKER_API_CONTAINER: "agents-core-web-api",
  OAC_WEB_DOCKER_USER: "501:20",
  OAC_WEB_DOCKER_CREDENTIALS_HOME_PATH: ".oac/web-smoke/executor-key.json",
  OAC_WEB_DOCKER_RUNTIME_HOME_PATH: ".oac/web-smoke/executors",
};

describe("local Docker guide configuration", () => {
  it("is disabled unless the operator opts in exactly", () => {
    expect(loadLocalDockerGuideProfile({})).toBeNull();
    expect(loadLocalDockerGuideProfile({ ...valid, OAC_WEB_DOCKER_GUIDE: "true" })).toBeNull();
  });

  it("accepts a complete non-secret local profile", () => {
    expect(loadLocalDockerGuideProfile(valid)).toEqual({
      image: valid.OAC_WEB_DOCKER_IMAGE,
      apiContainer: valid.OAC_WEB_DOCKER_API_CONTAINER,
      user: valid.OAC_WEB_DOCKER_USER,
      credentialsHomePath: valid.OAC_WEB_DOCKER_CREDENTIALS_HOME_PATH,
      runtimeHomePath: valid.OAC_WEB_DOCKER_RUNTIME_HOME_PATH,
    });
  });

  it("fails closed for partial or command-bearing values", () => {
    expect(() => loadLocalDockerGuideProfile({
      ...valid,
      OAC_WEB_DOCKER_IMAGE: "image; docker rm -f victim",
    })).toThrow("safe Docker image reference");
    expect(() => loadLocalDockerGuideProfile({
      ...valid,
      OAC_WEB_DOCKER_CREDENTIALS_HOME_PATH: "../executor-key.json",
    })).toThrow("safe HOME-relative path");
    expect(() => loadLocalDockerGuideProfile({
      ...valid,
      OAC_WEB_DOCKER_USER: "0:0",
    })).toThrow("numeric non-root");

    const partial: Record<string, string> = { ...valid };
    delete partial.OAC_WEB_DOCKER_RUNTIME_HOME_PATH;
    expect(() => loadLocalDockerGuideProfile(partial)).toThrow("OAC_WEB_DOCKER_RUNTIME_HOME_PATH is required");
  });
});

const validBackend = {
  OAC_WEB_DOCKER_BACKEND_GUIDE: "1",
  OAC_WEB_DOCKER_DATABASE_CONTAINER: "oac-web-smoke-db",
  OAC_WEB_DOCKER_API_CONTAINER: "agents-core-web-api",
  OAC_WEB_DOCKER_DAEMON_CONTAINER: "agents-core-web-daemon",
  OAC_WEB_DOCKER_CORE_PORT: "8091",
};

describe("local Docker backend guide configuration", () => {
  it("is disabled unless the operator explicitly opts in", () => {
    expect(loadLocalDockerBackendGuideProfile({})).toBeNull();
    expect(loadLocalDockerBackendGuideProfile({
      ...validBackend,
      OAC_WEB_DOCKER_BACKEND_GUIDE: "true",
    })).toBeNull();
  });

  it("accepts only non-secret container names and a loopback Core port", () => {
    expect(loadLocalDockerBackendGuideProfile(validBackend)).toEqual({
      databaseContainer: "oac-web-smoke-db",
      apiContainer: "agents-core-web-api",
      daemonContainer: "agents-core-web-daemon",
      corePort: 8091,
    });
  });

  it("fails closed for incomplete or command-bearing configuration", () => {
    expect(() => loadLocalDockerBackendGuideProfile({
      ...validBackend,
      OAC_WEB_DOCKER_DAEMON_CONTAINER: "daemon; docker rm victim",
    })).toThrow("safe Docker container name");
    expect(() => loadLocalDockerBackendGuideProfile({
      ...validBackend,
      OAC_WEB_DOCKER_CORE_PORT: "70000",
    })).toThrow("valid TCP port");

    const partial: Record<string, string> = { ...validBackend };
    delete partial.OAC_WEB_DOCKER_DATABASE_CONTAINER;
    expect(() => loadLocalDockerBackendGuideProfile(partial)).toThrow(
      "OAC_WEB_DOCKER_DATABASE_CONTAINER is required",
    );
  });
});
