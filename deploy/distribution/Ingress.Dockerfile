# The installer owns this image. Core and Web never receive Docker access.
FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d AS caddy
FROM docker:29.0.4-cli@sha256:858bb1e05af16840f5a55143c3e5e14073891fbc92f2c1f6f38dd9c5f2cca03c
RUN apk add --no-cache python3 ca-certificates
COPY --from=caddy /usr/bin/caddy /usr/local/bin/caddy
ENTRYPOINT []
