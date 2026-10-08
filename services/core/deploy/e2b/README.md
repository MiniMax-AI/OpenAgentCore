# E2B sandbox template

An E2B template packages a qualified sandbox image for Core-managed E2B sandboxes: the deployment selects E2B as its Sandbox Provider, and Core creates, renews and destroys sandboxes from the template. [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the selection, and the [E2B helper](../../tools/e2b-provider/README.md) owns the adapter and the startup script, [`managed_init.py`](managed_init.py), that starts the Sandbox I/O service in each sandbox.

## Build a template

Use Python 3.12 or newer, Docker and a qualified Linux amd64 sandbox image. Keep keys and build outputs outside the checkout, in private directories. Install the pinned SDK (`e2b` 2.51.0, [`requirements.txt`](requirements.txt)) and build:

```sh
python -m venv "$HOME/.oac/build/e2b-sdk"
"$HOME/.oac/build/e2b-sdk/bin/pip" install -r services/core/deploy/e2b/requirements.txt
"$HOME/.oac/build/e2b-sdk/bin/python" services/core/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_SANDBOX_IMAGE_DIGEST \
  --name your-sandbox-build \
  --api-key-file "$HOME/.oac/secrets/e2b.key" \
  --output "$HOME/.oac/build/e2b-template.json"
```

[`build-template.py`](build-template.py) requires a `sha256:` image ID and a Linux amd64 image built from the `sandbox` target of [`AgentHost.Dockerfile`](../../../../deploy/distribution/AgentHost.Dockerfile). It copies only `/usr/local/bin/oac-sandbox-io` onto a digest-pinned `node:22.23.1-bookworm-slim` base with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip`, `ripgrep` and `util-linux`. It installs [`managed_init.py`](managed_init.py) read-only under `/opt/oac-e2b`, makes UID/GID 1000 (`runtime`, home `/home/runtime`) the template user and builds with 2 vCPUs and 2048 MiB. The archive's `usr`, `usr/local` and `usr/local/bin` ancestors have traversable modes; the executable's mode, private build contexts, key inputs and the output's umask stay unchanged. Harnesses run on the separate agent host.


The output file records `template` (the immutable `templateID:build_UUID`), the source `image`, the packaged `runtime_sha256` and the `base`. Use that exact `template` value. Install system dependencies into the image; sandbox processes run as UID/GID 1000. No E2B account key or model credential belongs in a build, template environment, metadata, command argument or log.

## Tests

```sh
make check-e2b-provider
```

With `OAC_TEST_E2B_SDK_PYTHON` pointing at the pinned SDK environment, this runs this directory's tests and the helper's. They cover the startup input binding, the private Sandbox bootstrap file, credentials kept out of arguments and records, the one-shot claim and the template's archive modes. They create no billable resources. `make check-core` runs `managed_init_test.py` with only the standard library.
