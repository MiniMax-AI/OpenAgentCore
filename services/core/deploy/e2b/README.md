# E2B sandbox template

An E2B template packages a qualified Runtime image for Core-managed E2B sandboxes: the deployment selects E2B as its Sandbox Provider, and Core creates, renews and destroys sandboxes from the template. [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the selection, and the [E2B helper](../../tools/e2b-provider/README.md) owns the adapter and the startup script, [`managed_init.py`](managed_init.py), that starts the Sandbox I/O service in each sandbox.

## Build a template

Use Python 3.12 or newer, Docker and a qualified Linux amd64 Runtime image. Keep keys and build outputs outside the checkout, in private directories. Install the pinned SDK (`e2b` 2.51.0, [`requirements.txt`](requirements.txt)) and build:

```sh
python -m venv "$HOME/.oac/build/e2b-sdk"
"$HOME/.oac/build/e2b-sdk/bin/pip" install -r services/core/deploy/e2b/requirements.txt
"$HOME/.oac/build/e2b-sdk/bin/python" services/core/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_RUNTIME_IMAGE_DIGEST \
  --name your-runtime-build \
  --api-key-file "$HOME/.oac/secrets/e2b.key" \
  --output "$HOME/.oac/build/e2b-template.json"
```

[`build-template.py`](build-template.py) requires a `sha256:` image ID, a Linux amd64 image and the Runtime layout (`OAC_RUNTIME_WORKSPACE=/environment/workspace`). It copies the image's `/usr/local/bin`, `/opt` and, when present, `/usr/local/codex-resources` and `/etc/codex`, together with its `HOME` and `OAC_*` environment, onto a digest-pinned `node:22.23.1-bookworm-slim` base with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip`, `ripgrep` and `util-linux`. It installs [`managed_init.py`](managed_init.py) read-only under `/opt/oac-e2b`, makes UID/GID 1000 (`runtime`, home `/home/runtime`) the template user and builds with 2 vCPUs and 2048 MiB. Only the `usr`, `usr/local` and `etc` archive ancestors it creates get traversable modes; Runtime file modes, private build contexts, key inputs and the output's umask stay unchanged.

The output file records `template` (the immutable `templateID:build_UUID`), the source `image`, the packaged `runtime_sha256` and the `base`. Use that exact `template` value. The template must qualify every Harness its image advertises. Install system dependencies into the image; sandbox processes run as UID/GID 1000. No E2B account key or model credential belongs in a build, template environment, metadata, command argument or log.

## Tests

```sh
make check-e2b-provider
```

With `OAC_TEST_E2B_SDK_PYTHON` pointing at the pinned SDK environment, this runs this directory's tests and the helper's. They cover the startup input binding, the private Sandbox bootstrap file, credentials kept out of arguments and records, the one-shot claim and the template's archive modes. They create no billable resources. `make check-core` runs `managed_init_test.py` with only the standard library.
