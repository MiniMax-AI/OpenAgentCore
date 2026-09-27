# Build context contains the daemon, shared helpers and pinned native artifact.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip ripgrep bubblewrap socat \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY build-system-seed.py /tmp/build-system-seed.py
RUN python3 -I -S /tmp/build-system-seed.py && rm /tmp/build-system-seed.py
COPY --chmod=0555 tool-root.py /usr/local/bin/oac-tool-root
COPY --chmod=0555 oac-daemon oac-codex-directory oac-codex-write oac-workspace-export /usr/local/bin/
COPY mcode-harness /opt/mcode-harness
COPY --chmod=0444 runtime-initialize.py /usr/local/bin/oac-runtime-initialize
ENV HOME=/home/runtime OAC_RUNTIME_HOME=/home/runtime/.oac \
    OAC_RUNTIME_MCODE_NODE=/usr/local/bin/node \
    OAC_RUNTIME_MCODE_BIN=/opt/mcode-harness/native/cli.js \
    OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE=/opt/mcode-harness/bridge.mjs \
    OAC_RUNTIME_MCODE_AGENTS_API=1 \
    OAC_RUNTIME_MCODE_WORKSPACE=managed \
    OAC_RUNTIME_WORKSPACE=/environment/workspace \
    OAC_RUNTIME_DIRECTORY_HELPER=/usr/local/bin/oac-codex-directory \
    OAC_RUNTIME_WRITE_HELPER=/usr/local/bin/oac-codex-write \
    OAC_RUNTIME_EXPORT_HELPER=/usr/local/bin/oac-workspace-export \
    OAC_RUNTIME_STAGING=/environment/staging
USER 1000:1000
RUN node /opt/mcode-harness/check.mjs && /opt/mcode-harness/native/cli.js --version
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/oac-daemon"]
CMD ["connect", "--profile", "default"]
