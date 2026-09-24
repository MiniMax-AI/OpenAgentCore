# Linux amd64 runtime. Build with make docker-build-agents-api.
# The context contains independently built executables and the pinned cloud SDK helper.
FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgcc-s1 \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 agents-api agents-api-migrate agents-api-device agents-api-environment-key parsar-sandbox-node /usr/local/bin/
COPY e2b/ /opt/parsar/e2b/
ENV AGENTS_API_ADDR=:8091
ENV AGENTS_API_E2B_PROVIDER_BIN=/opt/parsar/e2b/agents-api-e2b-provider
EXPOSE 8091
USER 65532:65532
CMD ["/usr/local/bin/agents-api"]
