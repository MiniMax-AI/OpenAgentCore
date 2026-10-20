// Fields of the Agent's Feishu bot that an environment template may expose to tools.
export const botFields = {
  app_id: "App ID",
  app_secret: "App Secret",
  brand: "地区",
};
export const environmentVariableName = (name) =>
  /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) &&
  !/^(OAC_|CODEX_)/.test(name) &&
  !["PATH", "OPENAI_API_KEY"].includes(name);
