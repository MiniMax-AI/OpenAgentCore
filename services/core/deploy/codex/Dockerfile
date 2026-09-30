# Build context is a prepared binary bundle, never the product checkout.
# Native package and resources must both be Codex 0.153.4 (linux/amd64).
FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates bash git python3 python3-pip nodejs npm ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /environment/workspace /workspace /home/runtime \
    && groupadd --gid 1000 runtime \
    && useradd --uid 1000 --gid 1000 --home-dir /home/runtime --no-create-home runtime
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
