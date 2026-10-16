<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/openagentcore-wordmark-dark.svg">
  <img alt="OpenAgentCore" src="docs/assets/openagentcore-wordmark-light.svg" width="440">
</picture>

オープンソースでセルフホストできる OpenAI Agents API。公式 OpenAI SDK から、自分のマシンと自分のモデルで Codex、Claude Code、MiniMax Code を実行できます。

[![Release](https://img.shields.io/github/v/release/MiniMax-AI/OpenAgentCore)](https://github.com/MiniMax-AI/OpenAgentCore/releases/latest) [![License](https://img.shields.io/github/license/MiniMax-AI/OpenAgentCore)](LICENSE)

[公式サイト](https://openagentcore.dev/) · [ドキュメント](https://openagentcore.dev/docs/getting-started/) · [クイックスタート](#クイックスタート) · [コントリビューション](CONTRIBUTING.md)

[English](README.md) · [简体中文](README.zh-CN.md) · **日本語**

</div>

- **OpenAI と同じ API。** `OPENAI_BASE_URL` を設定すれば、[公式 OpenAI SDK](https://developers.openai.com/api/docs/libraries)または直接の HTTP リクエストがそのまま動作します。
- **自分のエージェント、自分のモデル。** 各 Session は Codex、Claude Code、MiniMax Code のいずれかをネイティブに実行し、設定したモデルプロバイダーを使用します。詳しくは [Harness の機能](https://openagentcore.dev/contracts/agents-api/harness-capabilities)をご覧ください。
- **計算資源は休止し、ワークスペースは残る。** microsandbox と独立したワークスペースストレージの組み合わせでは、アイドル状態のサンドボックスが計算資源を解放し、互換性のあるノードでファイルをそのまま再開します。E2B、Docker、自分の Linux、macOS、Windows マシンでも Session を実行できます。詳しくは[Sandbox Provider protocol](https://openagentcore.dev/docs/sandbox-provider)をご覧ください。
- **置き換え可能なコンポーネント。** サンドボックスプロバイダー、ワークスペースファイルシステム、Harness、モデルプロバイダーは、[定義されたプロトコル](AGENTS.md#protocols-at-every-boundary)を通じて接続されます。

![デプロイの概要](docs/assets/console-overview-en.webp)

## クイックスタート

[前提条件](https://openagentcore.dev/docs/getting-started/install#prerequisites)に従って Docker をセットアップした Linux または macOS で、Core、Web、PostgreSQL をインストールします。

```sh
curl -fsSL https://openagentcore.dev/install.sh | bash
```

Windows では PowerShell で実行します。

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

Web で、インストーラーが表示する Core key でサインインし、パブリックアドレスを設定し、デフォルトモデルを設定し、Project API key を発行して、サンドボックスの容量（ノードまたは E2B）を追加します。各手順は[Core と Web のインストール](https://openagentcore.dev/docs/getting-started/install)をご覧ください。

続いて OpenAI SDK から呼び出します。`OPENAI_BASE_URL` に API のベース URL（`https://core.example/v1`）、`OPENAI_API_KEY` に Project API key を設定してください。

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body={"agent": {"x_agents_core": {"harness": "codex"}}},
)
print(session.id)
```

[最初の Session を実行](https://openagentcore.dev/docs/getting-started/quickstart)では、結果を待って読み戻します。

## 仕組み

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/architecture-dark.svg">
  <img alt="OpenAgentCore のアーキテクチャ" src="docs/assets/architecture.svg">
</picture>

アプリケーションと管理者は、次の Core API を使用します。

| API | パス | 利用者 |
| --- | --- | --- |
| **[Agents API](https://openagentcore.dev/docs/api/public-agent-api)** | `/v1` | アプリケーション。[OpenAI の Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview) と同じプロトコルを使用 |
| **[Core API](https://openagentcore.dev/contracts/agents-api/admin-api)** | `/core/v1` | 管理者。Web 経由で利用 |

Core は実行状態を永続化し、Runtime は Environment 内で選択した Harness を実行します。各コンポーネントは定義されたプロトコルで接続されているため、個別に置き換えられます。詳しくは[アーキテクチャ](https://openagentcore.dev/docs/architecture)をご覧ください。

## ドキュメント

| 目的 | 参照先 |
| --- | --- |
| インストールと運用 | [Core と Web のインストール](https://openagentcore.dev/docs/getting-started/install)、続いて[運用](https://openagentcore.dev/docs/getting-started/operations) |
| API を使ったアプリケーション開発 | [最初の Session を実行](https://openagentcore.dev/docs/getting-started/quickstart)、続いて [Agents API ガイド](https://openagentcore.dev/docs/api/public-agent-api) |
| 完成したアプリケーションの確認 | [サンプル](https://openagentcore.dev/docs/examples) |
| 自分のマシンでエージェントを実行 | [セルフホスト実行](https://openagentcore.dev/docs/getting-started/self-hosted) |
| Harness の機能と制限の確認 | [Harness の機能](https://openagentcore.dev/contracts/agents-api/harness-capabilities) |
| 設計の理解 | [アーキテクチャ](https://openagentcore.dev/docs/architecture) |
| 新しいサンドボックス、Harness などのコンポーネントの追加 | [OpenAgentCore の開発](https://openagentcore.dev/docs/development) |

## コントリビューションとライセンス

コードを変更する前に [CONTRIBUTING.md](CONTRIBUTING.md) をお読みください。バグ報告と質問は [GitHub Issues](https://github.com/MiniMax-AI/OpenAgentCore/issues) へお寄せください。[MIT License](LICENSE) で公開しています。
