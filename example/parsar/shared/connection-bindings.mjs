// Fields a connection may expose to a tool environment. Refresh tokens and app secrets are never bindable.
export const connectionFields = {
  access_token: "访问令牌",
  client_id: "应用 ID",
  region: "区域",
};
export const environmentVariableName = (name) =>
  /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) &&
  !/^(OAC_|CODEX_)/.test(name) &&
  !["PATH", "OPENAI_API_KEY"].includes(name);
