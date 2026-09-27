SHELL := /bin/bash
SQLC_VERSION ?= v1.29.0
SQLC ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
SWAG_VERSION ?= v1.16.4

.PHONY: help check check-database check-go check-sqlc sqlc-generate node-deps check-claude-sdk check-web check-mcode-harness build-daemon build-agents-api build-agents-api-release check-agents-api docker-build-agents-api check-agents-api-container build-agents-executor check-agents-executor build-agents-runtime build-claude-runtime build-claude-sdk-runtime build-mcode-harness build-mcode-runtime

help:
	@printf '%s\n' 'make build-agents-api  Build standalone Core commands' 'make build-daemon      Build the execution daemon' 'make check             Run Core, persistence and runtime checks' 'See README.md for runtime prerequisites and deployment.'

check: check-distribution check-database check-sqlc check-go check-microsandbox-provider check-agents-api check-claude-sdk check-web check-mcode-harness check-agents-executor
	@printf 'Parsar Core checks passed.\n'

check-database:
	@if [[ -n "$${PARSAR_AGENTS_API_TEST_DATABASE_URL+x}" && -z "$${OAC_TEST_DATABASE_URL+x}" ]]; then \
	    echo 'PARSAR_AGENTS_API_TEST_DATABASE_URL was renamed; set OAC_TEST_DATABASE_URL instead' >&2; exit 1; \
	fi
	@test -n "$${OAC_TEST_DATABASE_URL:-}" || { echo 'Set OAC_TEST_DATABASE_URL to a dedicated test PostgreSQL database' >&2; exit 1; }

sqlc-generate:
	cd services/agents-api && $(SQLC) generate

SWAG ?= go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

.PHONY: openapi
openapi:
	@set -e; root="$${OAC_DEV_HOME:-$$HOME/.oac}/build"; mkdir -p "$$root"; \
	output=$$(mktemp -d "$$root/core-openapi.XXXXXX"); trap 'rm -rf "$$output"' EXIT; \
	$(SWAG) init \
	    -g cmd/server/main.go --dir ./services/agents-api,./contracts/agents-api/v1 \
	    --output "$$output" \
	    --outputTypes yaml --parseInternal; \
	python3 scripts/patch-agents-openapi.py "$$output/swagger.yaml"; \
	go run ./scripts/openapi-split "$$output/swagger.yaml" contracts/agents-api/openapi.yaml contracts/agents-api/core.openapi.yaml contracts/agents-api/runtime.openapi.yaml

check-sqlc:
	python3 scripts/check-sqlc.py

check-go:
	go test ./apps/parsar-daemon/... ./internal/... ./contracts/agents-api/... ./scripts/openapi-split -count=1

build-daemon:
	@set -e; output="$${OAC_DEV_HOME:-$$HOME/.oac}/build/daemon"; \
	[[ "$$output" == /* ]] || { echo 'Daemon output directory must be absolute' >&2; exit 1; }; \
	mkdir -p "$$output"; \
	CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$$output/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon

build-agents-api:
	./scripts/build-agents-api.sh

build-agents-api-release:
	./scripts/build-agents-api-release.sh

check-agents-api: build-agents-api
	go test ./services/agents-api/... ./packages/agents-client/... -count=1
	PYTHONDONTWRITEBYTECODE=1 python3 services/agents-api/deploy/runtime/initialize_receipt_test.py
	PYTHONDONTWRITEBYTECODE=1 python3 services/agents-api/deploy/e2b/managed_init_test.py

docker-build-agents-api:
	./scripts/build-agents-api-image.sh

check-agents-api-container: docker-build-agents-api
	OAC_DEV_CORE_IMAGE="$${OAC_DEV_CORE_IMAGE:-agents-api:dev}" OAC_TEST_SERVER_BIN="$(CURDIR)/services/agents-api/tests/container_server.py" $${OAC_TEST_OFFICIAL_SDK_PYTHON:-python3} services/agents-api/tests/official_client.py

node-deps:
	pnpm install --frozen-lockfile

check-claude-sdk: node-deps
	python3 services/agents-api/deploy/claude/shell_prefix_test.py
	pnpm --filter @parsar/claude-sdk-adapter test
	$(MAKE) build-claude-sdk-runtime

check-web: node-deps
	pnpm typecheck
	pnpm test:core-doctor
	pnpm test:web
	pnpm --filter @agents-core-web/web build
	pnpm test:web:acceptance

build-claude-sdk-runtime:
	./scripts/build-claude-sdk-runtime.sh

check-mcode-harness:
	node --test packages/mcode-harness/*.test.mjs
	@for script in packages/mcode-harness/*.mjs; do node --check "$$script"; done
	bash -n scripts/build-mcode-harness.sh scripts/build-mcode-runtime.sh

build-agents-executor:
	./scripts/build-agents-executor.sh

check-agents-executor:
	./scripts/check-agents-executor.sh

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
	cd services/agents-api/tools/microsandbox-provider; \
	GOWORK=off CGO_ENABLED=1 go build -mod=readonly -trimpath -o "$$output/agents-api-microsandbox-provider" .

check-microsandbox-provider:
	go test -mod=readonly ./services/agents-api/internal/sandbox/microsandbox/... -count=1
	@if [[ "$$(go env GOOS)" == linux ]]; then \
	    cd services/agents-api/tools/microsandbox-provider && GOWORK=off CGO_ENABLED=1 go test -mod=readonly ./... -count=1; \
	else \
	    printf 'Skipping the Linux-only microsandbox SDK helper tests; the full Linux gate is required before release.\n'; \
	fi

.PHONY: check-distribution build-core-distribution
check-distribution:
	go test ./services/core-console -count=1
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s deploy/install -p 'test_*.py'
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/core-distribution-manifest.test.py
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/config-reference.py --check
	bash -n deploy/install/install.sh scripts/build-core-console.sh scripts/build-core-distribution.sh scripts/prepare-release-runtimes.sh
	./scripts/build-core-console.sh

build-core-distribution:
	./scripts/build-core-distribution.sh

.PHONY: build-e2b-provider check-e2b-provider
build-e2b-provider:
	./scripts/build-e2b-provider.sh

# The pinned SDK environment is also tested when building the shipped helper.
check-e2b-provider:
	PYTHONDONTWRITEBYTECODE=1 $${OAC_TEST_E2B_SDK_PYTHON:-python3} -m unittest discover -s services/agents-api/deploy/e2b -p '*_test.py'
	PYTHONDONTWRITEBYTECODE=1 $${OAC_TEST_E2B_SDK_PYTHON:-python3} -m unittest discover -s services/agents-api/tools/e2b-provider -p '*_test.py'
