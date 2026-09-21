SHELL := /bin/bash
SQLC_VERSION ?= v1.29.0
SQLC ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
SWAG_VERSION ?= v1.16.4

.PHONY: help check check-database check-go check-sqlc sqlc-generate node-deps check-claude-sdk check-mcode-harness build-daemon build-agents-api build-agents-api-release check-agents-api docker-build-agents-api check-agents-api-container build-agents-executor check-agents-executor build-agents-harness check-agents-harness check-agents-harness-native build-agents-runtime build-claude-runtime build-claude-sdk-runtime build-mcode-harness build-mcode-runtime

help:
	@printf '%s\n' 'make build-agents-api  Build standalone Core commands' 'make build-daemon      Build the execution daemon' 'make check             Run Core, persistence and runtime checks' 'See README.md for runtime prerequisites and deployment.'

check: check-database check-sqlc check-go check-agents-api check-claude-sdk check-mcode-harness check-agents-executor check-agents-harness
	@printf 'Parsar Core checks passed.\n'

check-database:
	@test -n "$${PARSAR_AGENTS_API_TEST_DATABASE_URL:-}" || { echo 'Set PARSAR_AGENTS_API_TEST_DATABASE_URL to a dedicated test PostgreSQL database' >&2; exit 1; }

sqlc-generate:
	cd services/agents-api && $(SQLC) generate

.PHONY: openapi
openapi:
	@set -e; root="$${PARSAR_HOME:-$$HOME/.parsar}/build"; mkdir -p "$$root"; \
	output=$$(mktemp -d "$$root/core-openapi.XXXXXX"); trap 'rm -rf "$$output"' EXIT; \
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init \
	    -g cmd/server/main.go --dir ./services/agents-api,./contracts/agents-api/v1 \
	    --exclude ./services/agents-api/internal/executor --output "$$output" \
	    --outputTypes yaml --parseInternal; \
	mv "$$output/swagger.yaml" contracts/agents-api/openapi.yaml

check-sqlc:
	python3 scripts/check-sqlc.py

check-go:
	go test ./apps/parsar-daemon/... ./internal/... ./contracts/agents-api/... -count=1

build-daemon:
	@set -e; output="$${PARSAR_HOME:-$$HOME/.parsar}/build/daemon"; \
	[[ "$$output" == /* ]] || { echo 'Daemon output directory must be absolute' >&2; exit 1; }; \
	mkdir -p "$$output"; \
	CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$$output/parsar-daemon" ./apps/parsar-daemon/cmd/parsar-daemon

build-agents-api:
	./scripts/build-agents-api.sh

build-agents-api-release:
	./scripts/build-agents-api-release.sh

check-agents-api: build-agents-api
	go test ./services/agents-api/... ./packages/agents-client/... -count=1

docker-build-agents-api:
	./scripts/build-agents-api-image.sh

check-agents-api-container: docker-build-agents-api
	AGENTS_API_IMAGE="$${AGENTS_API_IMAGE:-agents-api:dev}" AGENTS_API_SERVER_BIN="$(CURDIR)/services/agents-api/tests/container_server.py" $${PARSAR_OFFICIAL_SDK_PYTHON:-python3} services/agents-api/tests/official_client.py

node-deps:
	pnpm install --frozen-lockfile

check-claude-sdk: node-deps
	python3 services/agents-api/deploy/claude/shell_prefix_test.py
	pnpm --filter @parsar/claude-sdk-adapter test
	$(MAKE) build-claude-sdk-runtime

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

build-agents-harness:
	./scripts/build-agents-harness.sh

check-agents-harness:
	./scripts/check-agents-harness.sh

check-agents-harness-native:
	./scripts/build-agents-harness.sh check

build-agents-runtime:
	./scripts/build-agents-runtime.sh

build-claude-runtime:
	./scripts/build-claude-runtime.sh

build-mcode-harness:
	./scripts/build-mcode-harness.sh

build-mcode-runtime:
	./scripts/build-mcode-runtime.sh
