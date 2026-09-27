#!/usr/bin/env python3
"""Count hosted and self-hosted Sessions that have no frozen model provider.

Read-only. Run it against an existing installation before upgrading to a Core that
stores deployment model providers itself: these Sessions cannot start new work
afterwards and must be recreated with x_agents_core.model_provider or an Agent that
has one saved. Cancelling their work and reading their history keep working.
Historical none Sessions are not counted: without a frozen provider they run with
their device's own environment.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys

ENVIRONMENTS = ("openai_hosted", "self_hosted")
# A Session froze a provider when Core marked its configuration at creation.
QUERY = """SELECT configuration->'environment'->>'type', count(*)
FROM sessions
WHERE deleted_at IS NULL
  AND configuration->'environment'->>'type' IN ('openai_hosted', 'self_hosted')
  AND COALESCE(configuration->>'model_provider_configured', '') <> 'true'
GROUP BY 1 ORDER BY 1"""


class CheckError(Exception):
    pass


def count(root):
    """Return {environment: count} for Sessions without a frozen provider."""
    # An installation made with config.json keeps its Compose file under generated/.
    compose = next((path for path in (Path(root) / "generated/compose.json", Path(root) / "compose.json")
                    if path.exists()), Path(root) / "compose.json")
    try:
        services = json.loads(compose.read_text())["services"]
    except (OSError, ValueError, KeyError, TypeError):
        raise CheckError("Cannot read " + str(compose) + "; pass the installation directory with --install-dir") from None
    if "database" not in services:
        raise CheckError("This installation has no Core database (Web-only); run the check on the Core installation")
    command = ["docker", "compose", "-f", str(compose), "exec", "-T",
               "-e", "PGOPTIONS=-c default_transaction_read_only=on", "database",
               "psql", "-X", "-q", "-A", "-t", "-F", "|", "-v", "ON_ERROR_STOP=1",
               "-U", "agents_api", "-d", "agents_api", "-c", QUERY]
    try:
        result = subprocess.run(command, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=60, check=False)
    except (OSError, subprocess.SubprocessError):
        raise CheckError("Cannot run the database query; is Docker available?") from None
    if result.returncode:
        raise CheckError("The database query failed; start the installation and retry")
    counts = dict.fromkeys(ENVIRONMENTS, 0)
    for line in result.stdout.splitlines():
        if not line.strip():
            continue
        environment, _, value = line.partition("|")
        if environment not in counts or not value.isdigit():
            raise CheckError("Unexpected database output")
        counts[environment] = int(value)
    return counts


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--install-dir", type=Path, default=Path.home() / ".oac/core")
    parser.add_argument("--json", action="store_true", help="print {environment: count} as JSON")
    args = parser.parse_args(argv)
    counts = count(args.install_dir)
    if args.json:
        print(json.dumps(counts, sort_keys=True))
        return
    if not any(counts.values()):
        print("Every hosted and self-hosted Session has a frozen model provider.")
        return
    print("Sessions without a frozen model provider (they cannot start new work after the upgrade):")
    for environment in ENVIRONMENTS:
        print(f"  {environment}: {counts[environment]}")
    print("Recreate them with x_agents_core.model_provider or an Agent that has one saved. "
          "Cancelling their work and reading their history keep working.")


if __name__ == "__main__":
    try:
        main()
    except CheckError as error:
        raise SystemExit(str(error)) from None
