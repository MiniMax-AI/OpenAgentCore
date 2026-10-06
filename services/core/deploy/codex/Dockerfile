# Build context is a prepared binary bundle, never the product checkout.
# Native package and resources must both be Codex 0.153.4 (linux/amd64).
# The base and package layer match the Claude and MiniMax images, so one build
# shares that layer and the combined Runtime image keeps the same base.
FROM node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime
COPY --chmod=0555 oac-daemon codex /usr/local/bin/
COPY codex-resources /usr/local/codex-resources
ENV HOME=/home/runtime OAC_RUNTIME_HOME=/home/runtime/.oac \
    OAC_RUNTIME_CODEX_BIN=/usr/local/bin/codex \
    OAC_RUNTIME_WORKSPACE=/environment/workspace \
    OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization \
    OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages
USER 1000:1000
RUN test "$(codex --version)" = "codex-cli 0.153.4"
WORKDIR /environment/workspace
ENTRYPOINT ["/usr/local/bin/oac-daemon"]
CMD ["connect", "--profile", "default"]
