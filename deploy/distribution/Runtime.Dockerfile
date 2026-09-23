# Each input is an immutable Linux amd64 image built from the same Core revision.
# Reuse the native packages and isolation configuration from existing profiles.
ARG CODEX_IMAGE
ARG CLAUDE_IMAGE
ARG MCODE_IMAGE
FROM ${CODEX_IMAGE} AS codex
FROM ${CLAUDE_IMAGE} AS claude
FROM ${MCODE_IMAGE}

# Keep the shared daemon, helpers and prebuilt tool-system seed from this base.
# Native harness packages remain outside the tool-system seed and workspace.
COPY --from=codex /usr/local/bin/codex /usr/local/bin/codex
COPY --from=codex /usr/local/codex-resources /usr/local/codex-resources
COPY --from=codex /etc/codex /etc/codex
COPY --from=claude /opt/claude-sdk /opt/claude-sdk
COPY --from=claude /usr/local/bin/agents-api-claude-shell-prefix /usr/local/bin/agents-api-claude-shell-prefix

ENV PARSAR_CODEX_BIN=/usr/local/bin/codex \
    PARSAR_CODEX_PERMISSION_PROFILE=managed-workspace \
    PARSAR_CLAUDE_SDK_NODE=/usr/local/bin/node \
    PARSAR_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js \
    PARSAR_CLAUDE_SDK_WORKSPACE=managed

USER 1000:1000
RUN test "$(codex --version)" = "codex-cli 0.153.4" \
    && test -r /etc/codex/requirements.toml \
    && node /opt/claude-sdk/dist/runtime_check.js /opt/claude-sdk/dist/main.js \
    && node /opt/mcode-harness/check.mjs \
    && /opt/mcode-harness/native/cli.js --version
