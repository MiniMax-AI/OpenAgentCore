# The context contains the static launcher, process shim and Sandbox I/O service,
# plus the validated Harness payloads in codex/, claude/ and mcode/.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27 AS base
USER root

# Sandbox tools are served by oac-sandbox-io; the caller supplies its bootstrap.
FROM base AS sandbox
# Login shells keep the process environment's package search path.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && sed -i 's|^if \[ "$(id -u)" -eq 0 \]; then$|if [ -n "${PATH:-}" ] \&\& /usr/bin/printenv PATH >/dev/null; then\n  :\nelif [ "$(/usr/bin/id -u)" -eq 0 ]; then|' /etc/profile \
    && mkdir -p /environment/workspace /environment/initialization /environment/packages /workspace /home/runtime \
    && chown 1000:1000 /environment/initialization /environment/packages
COPY --chmod=0555 oac-sandbox-io /usr/local/bin/
ENV HOME=/home/runtime
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/oac-sandbox-io", "--bootstrap-file"]

# Each Harness in its own directory, laid out as its agent.Installation
# expects. harnesses.json is the only record of where they are; the agent host
# reads it with agent.ManifestEnvironment.
FROM base AS agent-host
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --chmod=0555 oac-daemon oac-process-shim /opt/oac/bin/
COPY --chmod=0555 codex/codex codex/codex-code-mode-host /opt/oac/harnesses/codex/bin/
COPY codex/package.json /opt/oac/harnesses/codex/package.json
COPY claude/claude-sdk /opt/oac/harnesses/claude_sdk
COPY mcode/mcode-harness /opt/oac/harnesses/mcode
COPY <<EOF /opt/oac/harnesses.json
{"node": "/usr/local/bin/node", "harnesses": {"claude_sdk": "/opt/oac/harnesses/claude_sdk", "codex": "/opt/oac/harnesses/codex", "mcode": "/opt/oac/harnesses/mcode"}}
EOF
RUN test "$(/opt/oac/harnesses/codex/bin/codex --version)" = "codex-cli $(node -p 'require("/opt/oac/harnesses/codex/package.json").version.replace(/-linux-x64$/, "")')" \
    && node /opt/oac/harnesses/claude_sdk/dist/runtime_check.js /opt/oac/harnesses/claude_sdk/dist/main.js \
    && /opt/oac/harnesses/mcode/native/cli.js --version
ENTRYPOINT ["/opt/oac/bin/oac-daemon"]
