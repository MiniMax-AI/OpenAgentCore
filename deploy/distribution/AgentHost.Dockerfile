# The agent-host image and the sandbox image it works in. The context is what
# scripts/build-agent-host-images.sh prepares: the static oac-daemon,
# oac-process-shim and oac-sandbox-io, and the Runtime image builders' Harness
# payloads in codex/, claude/ and mcode/. Both images share the Runtime images'
# base and the sandbox shares their package layer.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27 AS base
USER root

# The tools a Runtime image gives a sandbox, served by oac-sandbox-io. The
# caller appends the bootstrap file's path.
FROM base AS sandbox
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY --chmod=0555 oac-sandbox-io /usr/local/bin/
ENV HOME=/home/runtime
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/oac-sandbox-io", "--bootstrap-file"]

# Each Harness in its own directory, laid out as its agent.Installation
# expects. harnesses.json is the only record of where they are; the agent host
# reads it with agent.ManifestEnvironment. The checks are the Runtime images'
# except MiniMax Code's companion check, which needs the tools that run in the
# sandbox here.
FROM base AS agent-host
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --chmod=0555 oac-daemon oac-process-shim /opt/oac/bin/
COPY --chmod=0555 codex/codex codex/codex-code-mode-host /opt/oac/harnesses/codex/bin/
COPY claude/claude-sdk /opt/oac/harnesses/claude_sdk
COPY mcode/mcode-harness /opt/oac/harnesses/mcode
COPY <<EOF /opt/oac/harnesses.json
{"node": "/usr/local/bin/node", "harnesses": {"claude_sdk": "/opt/oac/harnesses/claude_sdk", "codex": "/opt/oac/harnesses/codex", "mcode": "/opt/oac/harnesses/mcode"}}
EOF
RUN test "$(/opt/oac/harnesses/codex/bin/codex --version)" = "codex-cli 0.153.4" \
    && node /opt/oac/harnesses/claude_sdk/dist/runtime_check.js /opt/oac/harnesses/claude_sdk/dist/main.js \
    && /opt/oac/harnesses/mcode/native/cli.js --version
ENTRYPOINT ["/opt/oac/bin/oac-daemon"]
