# One-time data initialization. oac init runs as root so it can chown data
# directories, then exits. It downloads the node payload over HTTPS, so the
# image carries CA certificates. scripts/build-core-distribution.sh builds this
# from a context that also contains the oac binary.
FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache ca-certificates
COPY --chmod=0555 oac /usr/local/bin/oac
ENTRYPOINT []
