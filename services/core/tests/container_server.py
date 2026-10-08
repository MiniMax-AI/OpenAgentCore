#!/usr/bin/env python3
"""Run the existing official-client suite against a read-only Linux container."""

import os

# Match ownership of the suite's private installation files without granting root access.
# The image's default UID is separately exercised by deployment acceptance.
assert os.getuid() != 0, "Run container acceptance as an unprivileged host user"
args = [
    "docker", "run", "--rm", "--read-only", "--network=host",
    "--cap-drop=ALL", "--security-opt=no-new-privileges",
    "--user", f"{os.getuid()}:{os.getgid()}",
]
for name, target in (("OAC_CORE_KEY_DIGESTS_FILE", "/run/core-key-digests.json"),
                     ("OAC_CREDENTIAL_KEY_FILE", "/run/credential.key"),
                     ("OAC_INSTALLATION_ID_FILE", "/run/installation.id"),
                     ("OAC_AGENT_HOST_IDENTITY_FILE", "/run/agent-host/identity.json")):
    args.extend([
        "--mount", f"type=bind,source={os.environ[name]},target={target},readonly",
        "--env", f"{name}={target}",
    ])
for name in ("OAC_DATABASE_URL", "OAC_ADDR", "OAC_DEFAULT_HARNESS", "OAC_PUBLIC_URL"):
    args.extend(["--env", name])
args.append(os.environ["OAC_DEV_CORE_IMAGE"])
# Docker forwards termination to the API and --rm removes the stopped container.
os.execvp(args[0], args)
