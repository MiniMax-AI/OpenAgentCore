# Build context contains only the daemon, shared helpers and pinned SDK bundle.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY --chmod=0555 oac-daemon /usr/local/bin/
COPY claude-sdk /opt/claude-sdk
ENV HOME=/home/runtime OAC_RUNTIME_HOME=/home/runtime/.oac \
    OAC_RUNTIME_CLAUDE_SDK_NODE=/usr/local/bin/node \
    OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT=/opt/claude-sdk/dist/main.js \
    OAC_RUNTIME_CLAUDE_SDK_WORKSPACE=managed \
    OAC_RUNTIME_WORKSPACE=/environment/workspace \
    OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization \
    OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages
USER 1000:1000
RUN node /opt/claude-sdk/dist/runtime_check.js /opt/claude-sdk/dist/main.js
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/oac-daemon"]
CMD ["connect", "--profile", "default"]
