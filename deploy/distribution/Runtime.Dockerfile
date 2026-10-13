# Each input is an immutable Linux amd64 image built from the same Core revision.
# Reuse the native packages from existing profiles.
ARG CODEX_IMAGE
ARG CLAUDE_IMAGE
ARG MCODE_IMAGE
FROM ${CODEX_IMAGE} AS codex
FROM ${CLAUDE_IMAGE} AS claude
FROM ${MCODE_IMAGE}

# Keep the shared daemon and dependencies from the MiniMax base.
# Native harness packages remain outside the workspace.
COPY --from=codex /usr/local/bin/codex /usr/local/bin/codex
COPY --from=codex /usr/local/codex-resources /usr/local/codex-resources
COPY --from=claude /opt/claude-sdk /opt/claude-sdk

ENV OAC_RUNTIME_CODEX_BIN=/usr/local/bin/codex \
    OAC_RUNTIME_CLAUDE_SDK_NODE=/usr/local/bin/node \
    OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js

USER 1000:1000
RUN test "$(codex --version)" = "codex-cli 0.153.4" \
    && node /opt/claude-sdk/dist/runtime_check.js /opt/claude-sdk/dist/main.js \
    && node /opt/mcode-harness/check.mjs \
    && /opt/mcode-harness/native/cli.js --version
