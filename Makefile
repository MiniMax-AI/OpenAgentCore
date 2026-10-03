SHELL := /bin/bash
SQLC_VERSION ?= v1.29.0
SQLC ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
SWAG_VERSION ?= v1.16.4

.PHONY: help check check-database check-go check-sqlc sqlc-generate node-deps check-claude-sdk check-web check-mcode-harness build-daemon build-core check-core check-core-packages check-core-store docker-build-core check-core-container build-agents-runtime build-claude-runtime build-claude-sdk-runtime build-mcode-harness build-mcode-runtime

help:
	@printf '%s\n' 'make build-core        Build standalone Core commands' 'make build-daemon      Build the execution daemon' 'make check             Run Core, persistence and runtime checks' 'See README.md for runtime prerequisites and deployment.'

check: check-ci check-harness-catalog check-names check-distribution check-database check-sqlc check-go check-microsandbox-provider check-core check-claude-sdk check-web check-example check-mcode-harness
	@printf 'OpenAgentCore checks passed.\n'

.PHONY: generate-harness-catalog check-harness-catalog
generate-harness-catalog:
	python3 scripts/generate-harness-catalog.py

check-harness-catalog:
	python3 scripts/generate-harness-catalog.py --check
	python3 scripts/generate-harness-catalog.test.py

.PHONY: check-names
check-names:
	python3 scripts/check-names.test.py
	python3 scripts/check-names.py

check-database:
	@test -n "$${OAC_TEST_DATABASE_URL:-}" || { echo 'Set OAC_TEST_DATABASE_URL to a dedicated test PostgreSQL database' >&2; exit 1; }

sqlc-generate:
	cd services/core && $(SQLC) generate

SWAG ?= go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

.PHONY: openapi
openapi:
	@set -e; root="$${OAC_DEV_HOME:-$$HOME/.oac}/build"; mkdir -p "$$root"; \
	output=$$(mktemp -d "$$root/core-openapi.XXXXXX"); trap 'rm -rf "$$output"' EXIT; \
	$(SWAG) init \
	    -g cmd/server/main.go --dir ./services/core,./contracts/agents-api/v1 \
	    --output "$$output" \
	    --outputTypes yaml --parseInternal; \
	python3 scripts/patch-agents-openapi.py "$$output/swagger.yaml"; \
	go run ./scripts/openapi-split "$$output/swagger.yaml" contracts/agents-api/openapi.yaml contracts/agents-api/core.openapi.yaml contracts/agents-api/runtime.openapi.yaml

check-sqlc:
	python3 scripts/check-sqlc.py

check-go:
	go test ./apps/daemon/... ./internal/... ./contracts/agents-api/... ./scripts/openapi-split -count=1

.PHONY: check-runtime-contract
check-runtime-contract:
	go test ./internal/agentdaemon/proto/... ./services/core/internal/runtimegateway ./apps/daemon/internal/transport ./apps/daemon/internal/dispatch ./apps/daemon/internal/wireconformance -count=1
	go test ./services/core/internal/execution -run '^TestRuntimeProtocol' -count=1
	go test ./apps/daemon/internal/agent/... -run '^(TestSharedTextLifecycle|TestPublicHarnessContractDeclarations|TestRegistryRejectsEveryOmittedCapabilityBeforeReplacement|TestUnsupportedExtensionsHaveNoNativeEffects)$$' -count=1

build-daemon:
	@set -e; output="$${OAC_DEV_HOME:-$$HOME/.oac}/build/daemon"; \
	[[ "$$output" == /* ]] || { echo 'Daemon output directory must be absolute' >&2; exit 1; }; \
	mkdir -p "$$output"; \
	CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$$output/oac-daemon" ./apps/daemon/cmd/oac-daemon

build-core:
	./scripts/build-core.sh

CORE_STORE_PACKAGE := github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store

check-core: override OAC_CORE_STORE_SHARD :=
check-core: build-core check-core-packages check-core-store

# Core, service and client tests except the serial store integration package.
check-core-packages:
	@set -euo pipefail; packages=$$(go list ./services/core/... ./packages/agents-client/...); \
	packages=$$(printf '%s\n' "$$packages" | grep -vxF '$(CORE_STORE_PACKAGE)'); \
	go test $$packages -count=1 -timeout=20m
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s services/core/tests -p 'official_diagnostics_test.py'
	PYTHONDONTWRITEBYTECODE=1 python3 services/core/deploy/e2b/managed_init_test.py

# Store integration tests run serially and include bounded lifecycle waits that together exceed Go's 10m default.
# OAC_CORE_STORE_SHARD=INDEX/TOTAL runs one deterministic partition; unset runs them all.
check-core-store:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/go-test-shard.py $(CORE_STORE_PACKAGE) $(or $(OAC_CORE_STORE_SHARD),1/1) -count=1 -timeout=20m

# The distribution's Core image, without the native installer catalog.
docker-build-core:
	@set -e; root="$${OAC_DEV_HOME:-$$HOME/.oac}"; \
	mkdir -p "$$root/cache/oac-core-builds"; \
	context=$$(mktemp -d "$$root/cache/oac-core-builds/image.XXXXXX"); trap 'rm -rf "$$context"' EXIT; \
	./scripts/build-core-image-context.sh "$$context"; \
	docker build --platform linux/amd64 --tag "$${OAC_DEV_CORE_IMAGE:-oac-core:dev}" "$$context"

check-core-container: docker-build-core
	OAC_DEV_CORE_IMAGE="$${OAC_DEV_CORE_IMAGE:-oac-core:dev}" OAC_TEST_SERVER_BIN="$(CURDIR)/services/core/tests/container_server.py" $${OAC_TEST_OFFICIAL_SDK_PYTHON:-python3} services/core/tests/official_client.py

node-deps:
	pnpm install --frozen-lockfile
	pnpm --dir website install --frozen-lockfile
	pnpm --dir packages/claude-sdk-adapter install --frozen-lockfile

check-claude-sdk:
	pnpm --dir packages/claude-sdk-adapter install --frozen-lockfile
	pnpm --dir packages/claude-sdk-adapter test
	$(MAKE) build-claude-sdk-runtime

.PHONY: check-web-unit check-web-acceptance check-website
check-web: override OAC_WEB_TEST_SHARD :=
check-web: check-web-unit check-web-acceptance

web-deps:
	pnpm --filter @oac/web... install --frozen-lockfile

example-deps:
	pnpm --filter @oac/parsar-example... install --frozen-lockfile

.PHONY: web-deps example-deps
check-web-unit: web-deps
	pnpm --filter @oac/web... typecheck
	pnpm test:web
	pnpm --filter @oac/web build

# The website build also checks every published documentation link.
check-website:
	pnpm --dir website install --frozen-lockfile
	pnpm --dir website build
	pnpm --dir website test

# CI shards run in separate jobs, each with its own fixture and Web server.
# An unset shard keeps the complete local make check gate.
check-web-acceptance: web-deps
	pnpm test:web:acceptance $(if $(OAC_WEB_TEST_SHARD),--shard=$(OAC_WEB_TEST_SHARD))

build-claude-sdk-runtime:
	./scripts/build-claude-sdk-runtime.sh

.PHONY: check-example
check-example: example-deps
	pnpm --filter @oac/parsar-example typecheck
	pnpm --filter @oac/parsar-example test
	pnpm --filter @oac/parsar-example build
	pnpm --filter @oac/parsar-example test:e2e

check-mcode-harness:
	node --test packages/mcode-harness/*.test.mjs
	@for script in packages/mcode-harness/*.mjs; do node --check "$$script"; done
	bash -n scripts/build-mcode-harness.sh scripts/build-mcode-runtime.sh

build-agents-runtime:
	./scripts/build-agents-runtime.sh

build-claude-runtime:
	./scripts/build-claude-runtime.sh

build-mcode-harness:
	./scripts/build-mcode-harness.sh

build-mcode-runtime:
	./scripts/build-mcode-runtime.sh

.PHONY: build-microsandbox-provider check-microsandbox-provider
build-microsandbox-provider:
	@test "$$(go env GOOS)" = linux || { echo 'The microsandbox provider helper requires Linux' >&2; exit 1; }
	@set -e; output="$${OAC_DEV_HOME:-$$HOME/.oac}/build/microsandbox-provider"; \
	[[ "$$output" == /* ]] || { echo 'Provider output directory must be absolute' >&2; exit 1; }; \
	mkdir -p "$$output"; \
	cd services/core/tools/microsandbox-provider; \
	GOWORK=off CGO_ENABLED=1 go build -mod=readonly -trimpath -o "$$output/oac-microsandbox-provider" .

check-microsandbox-provider:
	go test -mod=readonly ./services/core/internal/sandbox/microsandbox/... -count=1
	@if [[ "$$(go env GOOS)" == linux ]]; then \
	    cd services/core/tools/microsandbox-provider && GOWORK=off CGO_ENABLED=1 go test -mod=readonly ./... -count=1; \
	else \
	    printf 'Skipping the Linux-only microsandbox SDK helper tests; the full Linux gate is required before release.\n'; \
	fi

.PHONY: check-distribution build-core-distribution
check-distribution:
	node --test scripts/build-native-catalog.test.mjs
	go test ./services/web -count=1
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/node -p 'test_*.py'
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/compose -p 'test_*.py'
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/acceptance -p 'test_*.py'
	PYTHONDONTWRITEBYTECODE=1 python3 deploy/test_install.py
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/core-distribution-manifest.test.py
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/publish-core-release.test.py
	bash -n deploy/install.sh scripts/build-web.sh scripts/build-core-distribution.sh scripts/build-core-image-context.sh scripts/prepare-release-runtimes.sh
	./scripts/build-web.sh

build-core-distribution:
	./scripts/build-core-distribution.sh

.PHONY: build-e2b-provider check-e2b-provider
build-e2b-provider:
	./scripts/build-e2b-provider.sh

# The pinned SDK environment is also tested when building the shipped helper.
check-e2b-provider:
	go run ./services/core/internal/sandbox/e2b/internal/contractgen --check
	PYTHONDONTWRITEBYTECODE=1 $${OAC_TEST_E2B_SDK_PYTHON:-python3} -m unittest discover -s services/core/deploy/e2b -p '*_test.py'
	PYTHONDONTWRITEBYTECODE=1 $${OAC_TEST_E2B_SDK_PYTHON:-python3} -m unittest discover -s services/core/tools/e2b-provider -p '*_test.py'

# These packages are also exercised by check-core in the full gate.
.PHONY: check-sandbox-provider-contract
check-sandbox-provider-contract:
	go test ./services/core/internal/sandbox/... -count=1

.PHONY: check-docs check-ci
check-docs:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/core-distribution-manifest.test.py BundledDocsTests

check-ci:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'ci_*test.py'
