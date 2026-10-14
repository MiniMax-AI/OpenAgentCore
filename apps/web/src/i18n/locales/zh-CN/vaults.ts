import type { vaults as english } from "../en/vaults";
type TranslationShape<T> = { [K in keyof T]: T[K] extends string ? string : TranslationShape<T[K]> };
export const vaults: TranslationShape<typeof english> = {
  securityNote: "令牌只可写入。此页面只接收 Credential 元数据，绝不会接收令牌值。删除 Vault 也会删除其中保存的所有 Credential，但不会在提供商撤销令牌。",
  loading: "正在加载 Vault", loadFailed: "无法加载 Vault", refreshFailed: "无法刷新 Vault",
  credential: "Credential", deleteVaultLabel: "删除 {{name}}", created: "创建时间", credentialsIn: "{{name}} 中的 Credential", oauthHelp: "OAuth · 请在你的应用中管理授权和令牌替换。", deleteCredentialLabel: "删除 {{name}}", noCredentials: "此 Vault 中还没有 Credential。", noVaults: "还没有 Vault", deleteVaultTitle: "删除 Vault？", deleteCredentialTitle: "删除 Credential？", deleting: "删除中…", delete: "删除", deleteVaultPrompt: "删除 {{name}} 及其中保存的全部 Credential？现有 Session 会保留冻结的 ID，但后续密钥读取会失败。提供商的令牌和正在运行的工作不会被撤销。", deleteCredentialPrompt: "删除 {{name}}？现有 Session 会保留其冻结标识，但后续密钥读取会失败。此操作不会撤销 MCP 提供商的令牌。", unnamed: "未命名 Vault",
  list: { label: "Vault", filterLabel: "筛选 Vault", filterPlaceholder: "按名称或 ID 筛选", name: "名称", created: "创建时间", actions: "操作", open: "打开 {{name}}", noMatch: "没有匹配的 Vault" },
  detail: { back: "返回", facts: "Vault 详情", id: "ID", created: "创建时间", metadata: "元数据", credentials: "Credential", credentialsHelp: "静态 bearer 令牌只可写入：Core 不会返回令牌，所以这里只显示元数据。替换令牌不会在提供商撤销旧令牌。", name: "名称", url: "MCP 服务 URL", auth: "认证方式", updated: "更新时间", addCredential: "添加 Credential", deleteVault: "删除 Vault", staticBearer: "静态 bearer", oauth: "OAuth", tokenHidden: "令牌已隐藏" },
};
