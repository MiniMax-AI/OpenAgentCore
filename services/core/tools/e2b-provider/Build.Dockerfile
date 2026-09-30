# Fixed CPython and glibc baseline shared by native Core and the Debian Core image.
FROM python:3.12.12-slim-bookworm@sha256:2986c55feb36e6cae00fa1fefb454283e4b33f35e75ff8bdd123b134130be301
RUN apt-get update && apt-get install -y --no-install-recommends binutils \
    && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["python3", "/source/services/core/tools/e2b-provider/build.py"]
