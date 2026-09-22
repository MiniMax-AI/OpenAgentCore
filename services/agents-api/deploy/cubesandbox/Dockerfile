# Managed Runtime image for a CubeSandbox microVM.
#
# Build context is the prepared Runtime bundle, never the product checkout (see
# scripts/build-agents-runtime.sh). The bundle is the same one the Docker profile
# uses, plus the readiness endpoint, so both backends run identical Runtime
# binaries.
#
# The base image is the vendor's digest-pinned environment, which provides envd
# on 49983 and the entrypoint contract this image relies on:
#   ghcr.io/tencentcloud/cubesandbox-base:2026.16
#   sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472
# The tag moved from the pinned revision only after re-resolving the digest in
# services/agents-api/internal/sandbox/cubesandbox/contract/README.md.
FROM ghcr.io/tencentcloud/cubesandbox-base:2026.16@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472

# The package list mirrors deploy/codex/Dockerfile, which is the source of truth
# for what the Runtime needs. The base image already installs envd, curl, sudo and
# tini; sudo is removed below because the managed profile denies privileged
# accounts to native tools (specification §8.7, R9), and the base image grants its
# uid-1000 account passwordless sudo.
#
# Node differs by base OS: Debian bookworm ships Node 18, Ubuntu 22.04 ships Node
# 12. The Codex Runtime does not invoke node directly, but any dependency that
# does must be verified against the packaged version during image qualification;
# deploy/cubesandbox/README.md records this as an open item.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates bash git python3 python3-pip nodejs npm ripgrep bubblewrap \
    && apt-get purge -y sudo \
    && rm -rf /var/lib/apt/lists/* /etc/sudoers.d/user \
    # Reuse the base image's uid/gid 1000 account under the Runtime's name and
    # home so the shared daemon, helper and profile paths are byte compatible
    # with the Docker profile.
    && groupmod --new-name runtime user \
    && usermod --login runtime --home /home/runtime --move-home runtime \
    && mkdir -p /environment/workspace /workspace /home/runtime

COPY build-system-seed.py /tmp/build-system-seed.py
RUN python3 -I -S /tmp/build-system-seed.py && rm /tmp/build-system-seed.py

COPY --chmod=0555 tool-root.py /usr/local/bin/agents-api-tool-root
COPY --chmod=0555 parsar-daemon agents-api-codex-directory agents-api-codex-write agents-api-workspace-export codex /usr/local/bin/
COPY --chmod=0555 parsar-runtime-readiness /usr/local/bin/parsar-runtime-readiness
COPY --chmod=0555 runtime-entrypoint.sh /usr/local/bin/parsar-runtime-entrypoint
COPY codex-resources /usr/local/codex-resources
COPY requirements.toml /etc/codex/requirements.toml
COPY --chmod=0444 tool-env.py /etc/codex/tool-env.py
RUN chmod 0555 /etc/codex && chmod 0444 /etc/codex/requirements.toml
COPY --chmod=0444 runtime-initialize.py /usr/local/bin/agents-api-runtime-initialize

# Identical to deploy/codex/Dockerfile. PARSAR_RUNTIME_READINESS_PORT documents the
# fixed probe port; the readiness endpoint reads it only for logs, the port itself
# is a compile-time constant shared with the adapter.
ENV HOME=/home/runtime PARSAR_HOME=/home/runtime/.parsar \
    ENVD_PORT=49983 \
    PARSAR_RUNTIME_READINESS_PORT=49984 \
    PARSAR_CODEX_BIN=/usr/local/bin/codex PARSAR_CODEX_PERMISSION_PROFILE=managed-workspace \
    PARSAR_RUNTIME_WORKSPACE=/environment/workspace \
    PARSAR_RUNTIME_DIRECTORY_HELPER=/usr/local/bin/agents-api-codex-directory \
    PARSAR_RUNTIME_WRITE_HELPER=/usr/local/bin/agents-api-codex-write \
    PARSAR_RUNTIME_EXPORT_HELPER=/usr/local/bin/agents-api-workspace-export \
    PARSAR_RUNTIME_STAGING=/environment/staging

USER 1000:1000
RUN test -r /etc/codex/requirements.toml && test "$(codex --version)" = "codex-cli 0.153.4"
WORKDIR /environment/workspace

# The vendor entrypoint starts envd in the background and execs the CMD, so the
# Runtime's own process starts after envd, exactly as the base image documents.
CMD ["/usr/local/bin/parsar-runtime-entrypoint"]
