# Core carries its cloud control-plane SDK helper. Node and Runtime payloads
# remain independently downloaded artifacts.
FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgcc-s1 \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 bin/oac-core bin/oac-core-migrate bin/oac-core-device bin/oac-core-environment-key /usr/local/bin/

COPY e2b/ /opt/oac/e2b/

ENV OAC_ADDR=:8091
ENV OAC_E2B_PROVIDER_BIN=/opt/oac/e2b/oac-e2b-provider
EXPOSE 8091
USER 65532:65532
CMD ["/usr/local/bin/oac-core"]
