# Pinned CubeSandbox contract

This directory pins the vendor contract the `cubesandbox` adapter compiles
against. It is data only: no Go code, no generated types. The adapter speaks the
vendor's own documented wire format with `net/http`, so the pin is evidence of
the exact revision it was written against rather than generated source.

`CUBESANDBOX-IMPLEMENTATION.md` §5.1 (milestone M0) requires the pin, and §5.1
step 3 requires the implementation specification to be corrected in the same
change wherever the pin disagrees with it. The corrections are listed under
"Reconciliation" below.

## Pinned files

| File | Source revision | Size (bytes) | SHA-256 |
| --- | --- | --- | --- |
| `openapi.yml` | `TencentCloud/CubeSandbox` @ `2f7d553e4bc6957621c4c4ae160c2b16e70df038` (2026-09-22), repository path `openapi.yml` | 67788 | `dc0d7fc56843897e87f1f963aae66eee40405bf4958f7ce9bc416e859b911338` |
| `process.proto` | `e2b-dev/infra` @ tag `2026.16` = `b8ca332f435370397bf42be614b2a5b620d65d39`, path `packages/envd/spec/process/process.proto` | 3299 | `8edd9358c7dbfcad96796b3f0ed8d14c262b8b14d6bc7d5e84d468941511b8e0` |
| `LICENSE-CubeSandbox` | same `TencentCloud/CubeSandbox` revision, path `LICENSE` (Apache-2.0 with the Tencent notice and third-party list) | 25798 | `ec4e72bcae6fbd76307517767f046f9c724fbf8aaee735b60310b21e46cd959a` |
| `LICENSE-envd` | same `e2b-dev/infra` revision, repository root `LICENSE` (Apache-2.0) | 11347 | `b4ef1bf811cb4095229fb86b574199e467c78f0ac4078cf8be62189e1fbd0818` |

`e2b-dev/infra@2026.16` is not an arbitrary choice: it is the envd ref that the
pinned CubeSandbox revision compiles its own base image from
(`.github/workflows/build-envd-base-image.yml` `ENVD_REF_DEFAULT`, and
`docker/Dockerfile.cube-base` `ARG ENVD_REF`). The proto bytes are identical to
the revision the retired Core E2B adapter vendored
(`fad70f393e800cee0278669a63976c3aaa00871b`, recorded SHA-256
`8edd9358c7dbfcad96796b3f0ed8d14c262b8b14d6bc7d5e84d468941511b8e0`), which is why
the same digest appears in this pin.

## Client-side facts the adapter relies on

These come from the pinned revision's own files; they are what makes a
dependency-free `net/http` adapter possible at all. Recorded here so a future
reader can re-derive them instead of reverse-engineering them again.

| Fact | Source (all at the pinned `TencentCloud/CubeSandbox` revision) | Size | SHA-256 |
| --- | --- | --- | --- |
| Connect streaming framing: 5-byte big-endian header, `0x01` compressed flag, `0x02` end-stream flag, `application/connect+json`, `Connect-Protocol-Version: 1` | `sdk/go/connect.go` | 2104 | `9cb2ce94ee670776f3a499de8b279ae69e7430ddff929b6c8b0829c88d09a373` |
| envd request shape, `POST /process.Process/Start`, response event schema, `POST /files?path=&username=`, Base64 stdout/stderr, end-event exit-code recovery from `status` | `sdk/go/envd.go` | 16932 | `60ac734eec81858f4c78709b526cb0d925e9fa6151df920b0829ff5b08079456` |
| Data-plane addressing `<port>-<sandboxID>.<domain>`, `EnvdPort = 49983`, `JupyterPort = 49999` | `sdk/go/sandbox.go` | 7584 | `984b814cc1c2b2baec6732834cb9aae82dd7c3acf3ff1bca8a5d8fc74068c1f7` |
| Data-plane transport: dial `ProxyNodeIP:ProxyPortHTTP` for every destination while preserving the virtual Host header | `sdk/go/transport.go` | 999 | `2ca76eb38cb6c351eeb066f5efeb6fb39e839890fae1686a9ca424ecf0eba55b` |
| `metadata["host-mount"]` is stripped from labels and forwarded to CubeMaster as a `host-mount` annotation; `filter_by_metadata` matches every `key=value` pair split on `&` and `=`; `envd_access_token` is `None` on create, detail and connect | `CubeAPI/src/services/sandboxes.rs` | 116356 | `bf6fcf4001e098e9c796023557a22fccec87addb6690607aaca60e95570d119a` |
| Base image entrypoint contract: envd starts in the background on `${ENVD_PORT:-49983}` with `-isnotfc` appended automatically | `docker/Dockerfile.cube-base`, `docker/cube-entrypoint.sh` | 3388 / 2614 | `f50a5d51c0515d389df9053149cd6a4c0300129ced7725308edf868cfceef4c8` / `5479737affc854261f16469fad1383c156baa89f6f2cecb0f294d26683fb5bc2` |

The same framing constants and the same "do not depend on a protobuf runtime"
choice appear in the vendor's Python SDK (`sdk/python/cubesandbox/_commands.py`,
SHA-256 `a13ee466e824593c2a57e36611d183de1b15c6a374fafb94c2fc442da59c417b`), so
the approach is vendor-sanctioned rather than a private reimplementation.

`stdin` input is not exercised by the vendor SDKs (they always send
`stdin: false`). The pinned proto documents the input channel — `SendInput` and
`CloseStdin` — and that is what this adapter uses, exactly as the retired Core
E2B adapter did, because Core's initialization payloads must never appear in
argv.

## Pinned base image

| Item | Value |
| --- | --- |
| Reference | `ghcr.io/tencentcloud/cubesandbox-base:2026.16` |
| Manifest index digest | `sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472` |
| Media type | `application/vnd.oci.image.index.v1+json` |

`deploy/cubesandbox/Dockerfile` pins the index digest, so the build is
reproducible for every architecture the vendor publishes. Re-resolve and update
this table when the tag moves.

## Reconciliation with `CUBESANDBOX-IMPLEMENTATION.md`

The pinned contract disagrees with the specification in the following places.
The specification was corrected in the same change; everything else in §5 was
confirmed by the pin.

1. **Sandbox state vocabulary (§2.2, §5.6).** The pinned `SandboxState` enum is
   `running | paused | pausing | unknown`. `resuming` and `terminated` are not
   part of it, and CubeAPI maps any unrecognised CubeMaster status to `running`.
   The adapter therefore treats only `running` as running and maps `paused`,
   `pausing` and `unknown` to a non-running value; absence is `404` and becomes
   `ErrNotFound`.
2. **TTL extension (§5.3).** The pin documents both
   `POST /sandboxes/{id}/refreshes` (`RefreshRequest{duration}`) and
   `POST /sandboxes/{id}/timeout` (`SetTimeoutRequest{timeout}`). `Renew` uses
   `/refreshes` with `duration = lease_seconds`, as §5.3 prefers.
3. **Delete statuses (§5.3, §6.7, §6.10).** Beyond `404`/`408`/`409`, the pin
   documents `500` and `503` with a `Retry-After` header for DELETE. `503`
   (sandbox pausing, another lifecycle operation in flight, insufficient Cubelet
   RPC time, or a failed internal resume) is a real failure, never a success.
4. **List surface (§5.3).** `GET /sandboxes` documents only a `metadata`
   parameter and returns the complete array; there is no v1 `state` filter and no
   v1 paging. `GET /v2/sandboxes` accepts `metadata`, `state`, `nextToken` and
   `limit`, but the pinned document describes no next-token response field or
   header, so v2 paging cannot be implemented from the pin alone. The adapter
   uses v1 with exact metadata filtering and treats the single response as
   complete, which is what §5.3 allows.
5. **`secure` (§5.4).** The field exists in `NewSandbox`, but the pin documents
   no semantics for it. The adapter does not set it.
6. **`envdAccessToken` (§5.5).** CubeAPI returns `envdAccessToken: null` from
   create, detail and connect. The adapter treats it as optional, never requires
   it and never logs it. The data plane is authenticated with the envd Basic user
   header; traffic-token headers are sent only when a token is actually present.
7. **Sandbox name (§6.3).** `NewSandbox` has no name field, so no name is
   derived or sent. Ownership is metadata-only, and the second `/workspace` view
   comes from the host-mount descriptors rather than from a named volume.
8. **Host mounts (§8.5) — confirmed, with the pin's extra constraints.**
   `metadata["host-mount"]` is a JSON-encoded array of
   `{hostPath, mountPath, readOnly}`. CubeMaster requires `hostPath` to be under
   an allowed prefix (`/data/shared/` by default, extended through
   `allowed_host_mount_prefixes`), compares it after `filepath.Clean` against
   prefixes ending in `/`, and rejects `/` as a prefix. Host mounts are
   node-local, are not copied into snapshots, pin pause/resume to the origin
   node, preserve the host directory's ownership and mode, and are not deleted
   with the sandbox.
9. **Egress (§8.6) — confirmed, with the pin's field names.**
   `SandboxNetworkConfig` carries `allowOut`, `allowPublicTraffic`, `denyOut`,
   `maskRequestHost` and L7 `rules`; `allow_internet_access` sits at the top
   level of `NewSandbox`. `allow_internet_access=false` installs `0.0.0.0/0` in
   `deny_out` and relies on `allowOut` entries to punch holes, which is exactly
   the `disabled` mapping this adapter needs.
10. **envd and the probe port (§8.1).** The pin's templates guide states that the
    probed port is the sandbox readiness contract afterwards, and that envd's
    `/health` returns `204`. The Runtime image keeps envd on `49983` and probes
    only its own readiness endpoint on the second fixed port, as §8.1 decides.

The readiness endpoint's port is a package constant rather than operator
configuration, because §8.1 fixes it for both the template build and the
adapter. `deploy/cubesandbox/README.md` records the same port.

