# Standalone container (advanced)

This is not the installation path for new users. To install Core with Web, nodes and
the `parsar` command, use the [installation guide](../../docs/getting-started/install.md).

This image packages the execution API, its embedded migrator and device operator
command. It needs a dedicated PostgreSQL database/account and an external daemon
for native execution. It contains no Parsar product service, frontend, product
migrations or harness. Supported protocol slices and execution limits remain as
listed in the [service guide](README.md) and [coverage](../../contracts/agents-api/README.md).
Container packaging does not imply complete protocol compatibility.

## Build

```bash
make docker-build-agents-api
# Optional local image name:
OAC_DEV_CORE_IMAGE=agents-api:local make docker-build-agents-api
```

The target needs Go, Docker and access to pinned Go modules and the base image.
It reuses the isolated binary build and sends only those executables, the E2B helper
and the image recipe to Docker. Linux amd64 is the current runtime target; other architectures
and registry publication are not included. The runtime base is the digest-pinned
`debian:bookworm-slim` image, with CA certificates and the glibc/libgcc runtime that
the bundled E2B helper needs. It keeps Debian's shell and package manager. The default
user is UID/GID 65532. No model credentials or tenant keys belong in the image.

## Configure and run

Use a new private directory for deployment configuration. Create `api.env` with
`AGENTS_API_DATABASE_URL` pointing to the dedicated execution database and
`AGENTS_API_CORE_KEY_DIGESTS_FILE=/run/core-key-digests.json`. Create
`core-key-digests.json` as a JSON array of Core key SHA-256 digests.
Keep the Core key separately and use the [administrator API](../../contracts/agents-api/admin-api.md)
to create Projects and issue application keys after startup. Keep both
files private, for example mode 0600 inside a mode 0700 directory. The database
hostname must be reachable from the container; container localhost is not the host.

Run migrations explicitly before starting the service. They belong to this API
alone and must never target the product database:

```bash
config_dir="$HOME/.parsar/agents-api-deployment"
docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --env-file "$config_dir/api.env" \
  agents-api:dev /usr/local/bin/agents-api-migrate
```

The following Linux example uses the non-root host UID to read its private key
file. Alternatively, grant the image's default UID read access and omit `--user`.
Do not run the example from a root shell.

```bash
docker run --name agents-api --detach --read-only \
  --cap-drop=ALL --security-opt=no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8091:8091 \
  --env-file "$config_dir/api.env" \
  --mount "type=bind,source=$config_dir/core-key-digests.json,target=/run/core-key-digests.json,readonly" \
  agents-api:dev
curl --fail http://127.0.0.1:8091/healthz
docker logs agents-api
```

The container listens on `:8091`; use a TLS reverse proxy for remote clients.
`/healthz` is liveness only. Database startup validation does not make it a
continuous readiness probe. Persistent API state is in PostgreSQL, so the image
needs no writable application volume. Send SIGTERM with `docker stop agents-api`
and start it again with `docker start agents-api`; never remove database storage
as part of replacing the API container. An execution worker currently permits one
active service per execution database; container replicas do not add HA/recovery.

## Connect execution

Set `AGENTS_API_PUBLIC_URL` in `api.env` to the API's externally reachable origin,
such as `https://core.example`, then start the container. Core derives the daemon
WebSocket URL from it. Provision a device using this image with
`/usr/local/bin/agents-api-device` as the command and the arguments documented in
[Internal execution device connection](README.md#internal-execution-device-connection).
Pass the same private environment file. The operator command emits a secret profile;
redirect it into a new private file and transfer it securely to the executor.

Install the daemon and native harness separately. Native history stays on that
executor; an API container restart must not be treated as a new native Session.
Model credentials belong in the executor's private configuration. API keys, device
credentials and Parsar user identities are separate. The daemon URL is not the
upstream `self_hosted.remote_url` protocol. Daemon distribution and product cutover
remain separate work.

## Verify

On Linux, with a non-root host user, an `oac_*_tests` database with API migrations applied
and the fixed official Python SDK installed:

```bash
OAC_TEST_DATABASE_URL='postgres://.../oac_local_tests' \
  OAC_TEST_OFFICIAL_SDK_PYTHON=python3 make check-agents-api-container
```

This reuses the existing SDK/raw-HTTP/Go-client suite against read-only containers,
including authentication, tenant isolation and restart persistence. Its host
network is a test convenience. Real daemon/model acceptance is additional evidence;
synthetic or HTTP-only checks do not prove native execution or full compatibility.

The image also includes `/usr/local/bin/agents-api-environment-key` for operator
issuance, rotation and revocation of exact-Environment executor credentials. Run it
with only the execution database configuration and the arguments in the
[native transport guide](README.md#user-managed-runtime-enrollment). Redirect
its secret stdout to a mode-0600 file under `~/.parsar/`; do not bake credentials
into the image or pass the broader caller key to an executor.
