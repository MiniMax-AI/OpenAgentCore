# Core is independent of execution payloads. Optional native Provider artifacts
# are downloaded separately; the Docker Provider is compiled into the API.
FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgcc-s1 \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 bin/agents-api bin/agents-api-migrate bin/agents-api-device bin/agents-api-environment-key /usr/local/bin/

ENV AGENTS_API_ADDR=:8091
EXPOSE 8091
USER 65532:65532
CMD ["/usr/local/bin/agents-api"]
