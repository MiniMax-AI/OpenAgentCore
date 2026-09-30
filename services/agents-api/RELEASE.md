# Standalone Core archive

This Linux amd64 archive holds Core alone: the API server `oac-core`, its migrator `oac-core-migrate`, and the operator commands `oac-core-device`, `oac-core-environment-key` and `oac-node`. It has no Web console, installer or `oac` command, and it needs your own PostgreSQL. To install Core with Web and nodes, use the [installation guide](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/install.md).

## Verify and extract

Verify the archive checksum supplied with the package, then extract it into a new directory under `~/.oac/`. Keep configuration outside the extracted package so that replacing the binaries does not replace credentials or state.

```sh
sha256sum -c @ARCHIVE_NAME@.tar.gz.sha256
mkdir -p "$HOME/.oac/releases"
tar -xzf @ARCHIVE_NAME@.tar.gz -C "$HOME/.oac/releases"
cd "$HOME/.oac/releases/@ARCHIVE_NAME@"
sha256sum -c SHA256SUMS
core_bin_dir="$PWD/bin"
```

`manifest.json` records the source commit and tree, the platform, the Go version, the pinned upstream protocol and the binary hashes. Checksums detect changed bytes; obtain the archive and its checksum from a trusted source.

## Configure and start

Create a dedicated PostgreSQL database and account. Generate a random Core key, keep it in private storage, and write its lowercase hex SHA-256 digest as a JSON array to `core-key-digests.json`:

```sh
umask 077
core_config_dir="$HOME/.oac/oac-core-deployment"
mkdir -p "$core_config_dir"
export OAC_DATABASE_URL='postgres://<account>:<password>@<host>/<database>'
export OAC_CORE_KEY_DIGESTS_FILE="$core_config_dir/core-key-digests.json"
export OAC_ADDR=127.0.0.1:8091
export OAC_PUBLIC_URL=http://127.0.0.1:8091
```

`OAC_DATABASE_URL` and `OAC_CORE_KEY_DIGESTS_FILE` are required. `OAC_PUBLIC_URL` is the origin that applications and machines use to reach Core: an HTTPS origin, or plain HTTP on loopback only. The [configuration reference](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/configuration.md#appendix-core-environment-without-the-installer) lists every variable. Run the migrations, then start Core in the foreground or under your own supervisor:

```sh
"$core_bin_dir/oac-core-migrate"
"$core_bin_dir/oac-core"
```

`GET /healthz` reports liveness. One Core process serves each database; replicas add no availability. Keep the database when you replace the binaries. Put a TLS reverse proxy in front for remote clients.

## Next steps

- Use the Core key with the [administrator API](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/admin-api.md) to create a Project and issue its API key, then follow the [quickstart](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/quickstart.md).
- To run Sessions on your own machines, follow the [self-hosted guide](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/self-hosted.md). The one-command installation needs the native installer catalog of the same commit: set `OAC_NATIVE_INSTALLER_DIR` to the `native-installers` directory of that commit's Core distribution, as the [configuration reference](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/configuration.md#appendix-core-environment-without-the-installer) describes. The [executor credential contract](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/environment-executor-credentials.md) covers issuing credentials with the Core key and `oac-core-environment-key`.
- To add sandbox nodes with `oac-node`, see the [node guide](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/nodes.md).
