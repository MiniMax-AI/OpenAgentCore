# One-time data initialization runs as root to prepare data ownership.
# Only the node installation metadata accompanies the oac binary.
FROM scratch
COPY --chmod=0555 oac /usr/local/bin/oac
COPY --chmod=0444 node-payload/ /opt/oac/node-payload/
ENTRYPOINT []
