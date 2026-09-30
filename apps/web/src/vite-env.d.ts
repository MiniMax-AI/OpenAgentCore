/// <reference types="vite/client" />

declare const __OAC_WEB_SELF_HOSTED_SESSIONS__: boolean;
declare const __OAC_WEB_OPENAI_HOSTED_SESSIONS__: boolean;
declare const __OAC_WEB_ENVIRONMENT_FILES__: boolean;
declare const __OAC_WEB_DOCKER_GUIDE__: null | {
  readonly image: string;
  readonly apiContainer: string;
  readonly user: string;
  readonly credentialsHomePath: string;
  readonly runtimeHomePath: string;
};
declare const __OAC_WEB_DOCKER_BACKEND_GUIDE__: null | {
  readonly databaseContainer: string;
  readonly apiContainer: string;
  readonly daemonContainer: string;
  readonly corePort: number;
};

interface ImportMetaEnv {
  readonly VITE_AGENT_MODEL_PRESETS?: string;
  readonly VITE_AGENT_DEFAULT_MODEL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
