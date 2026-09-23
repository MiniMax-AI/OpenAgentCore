# Core is independent of execution payloads. Optional native Provider artifacts
# are downloaded separately; the Docker Provider is compiled into the API.
FROM debian:bookworm-slim@sha256:a0982977ea1cf754c15281f48a7d5957cd14039a2a4f2aca9f23d74d226003d0

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgcc-s1 \
    && rm -rf /var/lib/apt/lists/*

COPY --chmod=0555 bin/agents-api bin/agents-api-migrate bin/agents-api-device bin/agents-api-environment-key /usr/local/bin/

ENV AGENTS_API_ADDR=:8091
EXPOSE 8091
USER 65532:65532
CMD ["/usr/local/bin/agents-api"]
