# Fixed CPython and glibc baseline shared by native Core and the Debian Core image.
FROM python:3.12.12-slim-bookworm@sha256:593bd06efe90efa80dc4eee3948be7c0fde4134606dd40d8dd8dbcade98e669c
RUN apt-get update && apt-get install -y --no-install-recommends binutils \
    && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["python3", "/source/services/core/tools/e2b-provider/build.py"]
