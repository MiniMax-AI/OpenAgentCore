<div align="center">

![OpenAgentCore — One core. Many agents.](docs/assets/openagentcore-banner.jpeg)

# OpenAgentCore

複数のネイティブ実行エンジンに対応し、自分のインフラにデプロイできる OpenAI Agents API のオープンソース実装です。

[公式サイト](https://openagentcore.dev/) · [インストール](#インストール) · [API を使う](https://openagentcore.dev/docs/getting-started/quickstart) · [ドキュメント](https://openagentcore.dev/docs/getting-started/) · [コントリビューション](CONTRIBUTING.md)

[English](README.md) · [简体中文](README.zh-CN.md) · **日本語**

</div>

## コンポーネントの関係

![OpenAgentCore のアーキテクチャ](docs/assets/architecture.png)

アプリケーションと管理者は、次の Core API を使用します。

| API | パス | 利用者 |
| --- | --- | --- |
| **[Agents API](https://openagentcore.dev/docs/api/public-agent-api)** | `/v1` | アプリケーション。[OpenAI の Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) と同じプロトコルを使用 |
| **[Core API](https://openagentcore.dev/contracts/agents-api/admin-api)** | `/core/v1` | 管理者。Web 経由で利用 |

Core は実行状態を永続化し、Runtime は Environment 内で選択した Harness を実行します。各コンポーネントは定義されたプロトコルで接続されているため、個別に置き換えられます。詳しくは[アーキテクチャガイド](https://openagentcore.dev/docs/architecture)をご覧ください。

## OpenAgentCore とは

OpenAgentCore は、自分のインフラ上で AI エージェントを実行し、[OpenAI Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) を提供します。

- **OpenAI と同じ API。** [公式 OpenAI SDK](https://developers.openai.com/api/docs/guides/agents/sdk) または直接の HTTP リクエストで、接続先を変更するだけで利用できます。新しいクライアントを覚える必要はありません。
- **エージェントを選択可能。** 各 [Session](https://openagentcore.dev/docs/api/public-agent-api) は、[Codex](https://github.com/openai/codex)、[Claude Code](https://code.claude.com/docs/en/overview)、[MiniMax Code](https://github.com/MiniMax-AI/minimax-code) のいずれかの[ネイティブ Harness](https://openagentcore.dev/contracts/agents-api/harness-onboarding) を実行し、[設定したモデルプロバイダー](https://openagentcore.dev/contracts/agents-api/model-execution)を使用します。
- **実行するマシンを選択可能。** エージェントは、[マネージドサンドボックス](https://openagentcore.dev/contracts/agents-api/sandbox-deployment)（[Docker](https://www.docker.com/)、[microsandbox](https://github.com/zerocore-ai/microsandbox)、[E2B](https://e2b.dev/)）でも、自分の Linux、macOS、Windows マシンでも動作します。
- **すべてのコンポーネントを置き換え可能。** [サンドボックス](https://openagentcore.dev/docs/sandbox-provider)、[Harness](https://openagentcore.dev/contracts/agents-api/harness-onboarding)、[モデルプロバイダー](https://openagentcore.dev/contracts/agents-api/model-execution)は、[定義されたプロトコル](AGENTS.md#protocols-at-every-boundary)を通じて接続されます。

## スクリーンショット

| 概要 | エージェントの監視 |
| --- | --- |
| ![デプロイの概要](docs/assets/console-overview-en.webp) | ![エージェントの監視](docs/assets/console-agent-metrics-en.webp) |

## インストール

[前提条件](https://openagentcore.dev/docs/getting-started/install#prerequisites)に従って Docker をセットアップした Linux または macOS で、次のコマンドを実行します。

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

Windows PowerShell の場合：

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

続いて、以下の手順で設定します。

1. インストーラーが生成した Core key で **Web（管理コンソール）にサインイン**し、**ドメインと HTTPS を設定**します。
2. **デフォルトモデルを設定**し、**Project API key を発行**します。
3. ノード、E2B、または自分のマシンを**実行リソースとして追加**します。
4. OpenAI SDK で **[最初の Session を実行](https://openagentcore.dev/docs/getting-started/quickstart)** します。

[インストールガイド](https://openagentcore.dev/docs/getting-started/install)では、各手順に加え、HTTPS の設定とローカルで手軽に試す方法を説明しています。リッスンアドレスやポートなどの設定は、[インストールオプション](https://openagentcore.dev/docs/getting-started/install-options)をご覧ください。

## ドキュメント

| 目的 | 参照先 |
| --- | --- |
| インストールと運用 | [インストールガイド](https://openagentcore.dev/docs/getting-started/install)、続いて[運用ガイド](https://openagentcore.dev/docs/getting-started/operations) |
| API を使ったアプリケーション開発 | [クイックスタート](https://openagentcore.dev/docs/getting-started/quickstart)、続いて [Agents API ガイド](https://openagentcore.dev/docs/api/public-agent-api) |
| 完成したアプリケーションの確認 | [サンプル](https://openagentcore.dev/docs/examples) |
| 自分のマシンでエージェントを実行 | [セルフホスト実行](https://openagentcore.dev/docs/getting-started/self-hosted) |
| Harness の機能と制限の確認 | [Harness の機能](https://openagentcore.dev/contracts/agents-api/harness-capabilities) |
| 設計の理解 | [アーキテクチャガイド](https://openagentcore.dev/docs/architecture) |
| 新しいサンドボックス、Harness などのコンポーネントの追加 | [開発ガイド](https://openagentcore.dev/docs/development) |

すべてのドキュメントは[ドキュメント一覧](https://openagentcore.dev/docs/getting-started/)から参照できます。コードを変更する前に、[コントリビューションガイド](CONTRIBUTING.md)をお読みください。
