# Core service

`services/core` is Core: one Go service that serves the Agents API (`/v1`), the Core API (`/core/v1`) and the machine connection routes (`/api/v1`), owns its PostgreSQL schema, and runs the execution Worker that dispatches Turns to Runtimes. [Architecture](../../docs/architecture.md) describes its role, and the [API index](../../docs/api/README.md) its namespaces and credentials. This guide is for contributors who build, run and test Core from source; operators install it with the [installer](../../docs/getting-started/install.md). The [implementation constraints](IMPLEMENTATION.md) hold the code-level rules.

## Commands

| Package | Executable | Purpose |
| --- | --- | --- |
| `cmd/server` | `oac-core` | The HTTP service and execution Worker |
| `cmd/migrate` | `oac-core-migrate` | Applies the embedded database migrations |
| `cmd/device` | `oac-core-device` | Provisions or revokes an [operator device profile](../../contracts/agents-api/machine-api.md#operator-device-profile) for `environment: none` engine hosts |
| `cmd/environment-key` | `oac-core-environment-key` | The [break-glass executor credential command](../../contracts/agents-api/environment-executor-credentials.md#break-glass-command) |
| `cmd/sandbox-node` | `oac-node` | The sandbox node program; see the [nodes guide](../../docs/getting-started/nodes.md) |
| `cmd/specification-contract` | None | Regenerates the installer's node specification projection |

`make build-core` builds the five executables into `~/.oac/build/oac-core`; [Standalone Core builds](../../docs/maintainers.md#standalone-core-builds) describes the build and its options. `make build-daemon` builds `oac-daemon`.

## Database

Core uses its own PostgreSQL database and account and shares no tables with an application. Its migrations are embedded goose migrations in [`migrations/`](migrations), tracked in `agents_api_schema_version`; a migration is never edited after it lands. Queries live in `internal/db/queries` and generate typed code with sqlc: run `make sqlc-generate` after changing a query and commit the generated files, which `make check-sqlc` compares.

## Run from source

1. Create a development database and apply the migrations:

   ```sh
   OAC_DATABASE_URL='postgres://oac:…@127.0.0.1:5432/oac_dev' go run ./services/core/cmd/migrate
   ```

2. Create a Core key of at least 32 characters and a digest file holding its SHA-256, which Core uses to authenticate `/core/v1`:

   ```sh
   umask 077; mkdir -p ~/.oac/dev
   openssl rand -hex 32 > ~/.oac/dev/core.key
   printf '["%s"]\n' "$(tr -d '\n' < ~/.oac/dev/core.key | sha256sum | cut -d' ' -f1)" > ~/.oac/dev/core-key-digests.json
   ```

3. Start Core. `OAC_PUBLIC_URL` enables the Runtime gateway and the Worker; without it Core executes nothing. The [Core environment table](../../docs/configuration.md#appendix-core-environment-without-the-installer) lists every variable.

   ```sh
   OAC_DATABASE_URL='postgres://oac:…@127.0.0.1:5432/oac_dev' \
   OAC_CORE_KEY_DIGESTS_FILE="$HOME/.oac/dev/core-key-digests.json" \
   OAC_PUBLIC_URL=http://127.0.0.1:8091 \
   go run ./services/core/cmd/server
   ```

4. Create a Project and an API key with the Core key, as in [Script the Core API](../../docs/getting-started/operations.md#script-the-core-api), and call `/v1` with the key as in the [quickstart](../../docs/getting-started/quickstart.md).
5. To run `environment: none` Sessions, provision an [operator device profile](../../contracts/agents-api/machine-api.md#operator-device-profile) for the Project's tenant and start `oac-daemon connect --profile default` on a host with a Harness installed. Self-hosted Sessions use the [self-hosted guide](../../docs/getting-started/self-hosted.md) instead.

## Tests

`make check-core` builds Core and runs the Go tests of `services/core` and `packages/agents-client`. Point `OAC_TEST_DATABASE_URL` at a dedicated database named `oac_*_tests` that holds no other tables; the tests apply only Core's migrations and use fresh tenants without truncating anything. Without it, database tests skip locally; CI provides its own PostgreSQL. Set `OAC_TEST_OFFICIAL_SDK_PYTHON` to the interpreter with the pinned SDK for the store's client fixtures. [Validate a change](../../docs/development.md#validate-a-change) lists the focused checks for other areas.

### Official client verification

The official-client suite runs the built server against the pinned OpenAI SDK from [`upstream.json`](../../contracts/agents-api/upstream.json), with the same test database, temporary keys and fresh tenants, and needs no model provider:

```sh
python -m pip install -r services/core/tests/requirements.txt
make build-core
OAC_TEST_DATABASE_URL='postgres://…/oac_local_tests' \
OAC_TEST_SERVER_BIN="${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core/oac-core" \
  python services/core/tests/official_client.py
```

It checks the upstream and generated response schemas, retries, ordering, tenant isolation, unsupported options and reads after a restart. It also runs the [official Go client](../../packages/agents-client/README.md#go-client) against two fresh tenants and validates the Sessions it created through the Python SDK.

## Generated contracts

Handler annotations generate the OpenAPI documents. Run `make openapi` after changing them: it runs the pinned swaggo generator and splits the result by namespace into [`openapi.yaml`](../../contracts/agents-api/openapi.yaml) (`/v1`), [`core.openapi.yaml`](../../contracts/agents-api/core.openapi.yaml) (`/core/v1`) and [`runtime.openapi.yaml`](../../contracts/agents-api/runtime.openapi.yaml) (`/api/v1`), each with only the security schemes its operations use. Review and commit the three diffs. Contract tests hold `openapi.yaml` to the pinned routes and fields, and hold all three to exactly the routes the server registers.
