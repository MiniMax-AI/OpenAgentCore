# OpenSandbox Runtime image and adapter

Core's OpenSandbox Sandbox Provider ([`sandbox/opensandbox`](../../internal/sandbox/opensandbox)) runs Core-managed sandboxes on your own [OpenSandbox](https://open-sandbox.ai/) server, on Docker or Kubernetes, through the official OpenSandbox Go SDK 1.2.0. Core needs no nodes and runs no helper process. [Sandbox Provider protocol](../../../../docs/sandbox-provider.md) owns the contract this adapter implements, and [Choose a sandbox backend](../../../../docs/getting-started/sandbox-backends.md#use-opensandbox) the steps in Web. OpenSandbox's own documentation covers its installation, runtimes and security; this page covers only what OpenAgentCore needs.

## Server requirements

| Setting | Requirement |
| --- | --- |
| Version | OpenSandbox release 1.2.0 or newer |
| `server.api_key` | Set; Core sends it as `OPEN-SANDBOX-API-KEY` ([Configuration](https://open-sandbox.ai/getting-started/configuration)) |
| `[egress] mode` | `dns+nft`. Core's network policy needs packet-level enforcement ([Egress](https://open-sandbox.ai/architecture/network/egress)), and Create fails on a server that reports another mode |
| `server.max_sandbox_timeout_seconds` | Unset or at least 3600: Core creates each sandbox with a one-hour lease and renews it |
| `[docker] publish_host` | `127.0.0.1` on the Docker runtime, with the server on the host network. execd answers on each sandbox's published port without authentication, and Core reaches it only through the server's proxy |
| Kubernetes | Pod and service ranges outside the private ranges added to the egress `deny.always` list ([Network isolation](https://open-sandbox.ai/guides/network-isolation)); the adapter denies only private, shared and link-local ranges |
| Network | Core reaches the server URL; sandboxes reach Core's [public address](../../../../docs/getting-started/public-address.md), which must resolve to global addresses ([network cap](../../../../contracts/agents-api/sandbox-deployment.md#network)) |

The isolation is OpenSandbox's: its Docker runtime runs plain containers, and a [secure container runtime](https://open-sandbox.ai/guides/secure-container) gives each sandbox its own kernel. The adapter does not request [Secure Access](https://open-sandbox.ai/guides/secure-access), so restrict who can reach the server and its ingress.

## Build the Runtime image

OpenSandbox runs a sandbox as its image's user, and the Runtime runs as UID 1000. [`Dockerfile`](Dockerfile) gives that user the Runtime directories and links `/workspace` to `/environment/workspace`, so the sandbox needs no root. Build it from the Runtime image of the release your Core runs and push it where your OpenSandbox server pulls images:

```sh
docker load -i oac-<version>-linux-amd64-runtime.tar.gz   # prints the loaded image ID
docker tag <loaded-image-id> oac-runtime:<version>
docker build --build-arg RUNTIME_IMAGE=oac-runtime:<version> \
  -t registry.example.com/oac/runtime-opensandbox:<version> services/core/deploy/opensandbox
docker push registry.example.com/oac/runtime-opensandbox:<version>
```

Rebuild it with every Core release, pull it on every OpenSandbox host, then select it in the deployment: Core gives each Create 30 seconds, which a pull inside the create request can exceed. The image is deployment configuration, not a public Environment Template.

## Operations

| Protocol operation | OpenSandbox call |
| --- | --- |
| `Create` | `POST /sandboxes` with the image, entrypoint `sleep infinity`, a one-hour timeout, the CPU and memory limits, the ownership metadata and the network policy. Through execd it then writes the [Runtime bootstrap](../../../../docs/runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600), runs `oac-daemon connect --profile default --bootstrap-file … -b` and records `io.oac.bootstrap: complete` in the metadata |
| `GetInfo` | Lists sandboxes by the allocation's metadata and checks the image |
| `Renew` | `POST /sandboxes/{id}/renew-expiration` to one hour from now, only while the sandbox is running |
| `Kill` | Deletes every sandbox with the allocation's metadata and lists again to confirm |
| `RunCommand` | execd `POST /command` as the Runtime user; confidential input goes through a private file that the command reads and removes |
| `Observe` | execd `GET /metrics`; see [Runtime observability](../../../../contracts/agents-api/runtime-observability.md) |

Every sandbox carries `io.oac.installation`, `io.oac.tenant`, `io.oac.environment` and `io.oac.allocation`, and the adapter acts only on a sandbox whose four values match. It keeps no local state. One create request yields at most one sandbox, every later step of the attempt runs inside it, and Kill removes it, so an owned sandbox observed in any state settles the creation; Core records that before cleanup. A create that never reaches the server, because the dial, name lookup or certificate check fails, settles as absent. When the server answers with its own error, its rollback has finished, so the adapter lists the allocation: an empty listing settles it as absent, and a sandbox that outlived the rollback settles it for cleanup. Any other unknown outcome stays unsettled until a sandbox appears, and the one-hour timeout ends a late one.

The checkpoint operations are not mapped. OpenSandbox's [pause](https://open-sandbox.ai/guides/pause-resume) differs by runtime: Docker freezes the container in place, Kubernetes keeps only the root filesystem, and only Fast Sandbox checkpoints memory. The preparation cache is not mapped either. [Network egress](../../../../docs/sandbox-provider.md#network-egress) describes how Core's egress maps to the network policy.

## Tests

`go test ./services/core/internal/sandbox/opensandbox` runs the shared contract suite against a local fake of the lifecycle and execd routes, together with the network-policy, configuration, command and observation tests. Native acceptance needs a real OpenSandbox server.
