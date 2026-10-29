// The bound Feishu bot's identity reaches every conversation of its Agent as
// these variables. Only the bot supplies them.
export const botVariables = {
  FEISHU_APP_ID: "app_id",
  FEISHU_APP_SECRET: "app_secret",
  FEISHU_BRAND: "brand",
};
export const environmentVariableName = (name) =>
  /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) &&
  !/^(OAC_|CODEX_)/.test(name) &&
  !["PATH", "OPENAI_API_KEY"].includes(name);
