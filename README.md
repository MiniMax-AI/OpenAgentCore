# Parsar Core

Standalone Agent API Core and its execution runtimes, copied from
[Parsar](https://github.com/MiniMax-AI-Dev/parsar) at
[`72ab4d37`](https://github.com/MiniMax-AI-Dev/parsar/commit/72ab4d37d49245f15b63d34f5741780e540bcec0).
The source repository retains both its product and its existing Core copy.

This repository contains the API service, PostgreSQL migrations, pinned public
protocol, execution daemon, the Docker provider, native Harness adapters,
runtime image builders, client library, tests and operator documentation.
It does not contain the Parsar web application, product backend, product database,
business CLI or product deployment stack.

V1 user-managed deployments colocate our daemon, selected harness, tools and
`/workspace`. Core manages Docker only; users provision, renew and destroy E2B
through the official SDK. The returned `remote_url` uses our private daemon
transport, not stock `exec-server`. See the
[Runtime enrollment guide](services/agents-api/README.md#user-managed-runtime-enrollment)
for harness enablement and the [qualification record](contracts/agents-api/user-managed-runtime-v1.md)
for tested deployments and remaining limits.

## Start here

- [API setup, authentication and execution](services/agents-api/README.md)
- [Standalone containers](services/agents-api/CONTAINER.md)
- [Docker Runtime](services/agents-api/deploy/codex/README.md)
- [Protocol coverage and known gaps](contracts/agents-api/README.md)
- [Harness selection](contracts/agents-api/harness-selection.md)
- [Contributor rules](CONTRIBUTING.md)
- [Copy provenance and validation](provenance/README.md)

```sh
make build-agents-api
make build-daemon
```

These builds require the Go version pinned in `go.mod`. Output goes under
`~/.parsar/build/`; no product checkout, frontend or product database is needed.
Provision a dedicated Core PostgreSQL database and caller credentials using the
operator guide before starting the service. Native execution also needs the
appropriate Runtime image and provider configuration.

The copied Go module/import paths, executable names and `PARSAR_*` environment
variables intentionally retain their existing names. They resolve to source in
this checkout, not a dependency on the Parsar product repository. This extraction
does not rename protocols or change execution behavior. Third-party native
sources and packages remain pinned dependencies, not vendored binaries.

## Validate

On Linux with Go, Node 22, pnpm 10.30.3, Python 3.9+, Rust 1.95.0 (including
rustfmt/Clippy), OpenSSL development libraries and a dedicated test PostgreSQL:

```sh
export PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_core_tests?sslmode=disable'
make check
```

The full gate requires the test database rather than silently skipping persistence
tests. Native model/provider fixtures remain explicit, credential-dependent
acceptance checks; see the [coverage ledger](contracts/agents-api/README.md).
The Rust gate covers only the retained directory/write/export helpers; the former
separate Codex harness gate and remote probe suite are retired.
Importing existing implementations does not establish additional protocol coverage.
