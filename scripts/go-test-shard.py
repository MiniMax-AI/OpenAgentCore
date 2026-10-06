#!/usr/bin/env python3
"""Run one deterministic partition of a Go package's top-level tests.

Usage: go-test-shard.py PACKAGE INDEX/TOTAL [go test flags...]

Each top-level test, example and fuzz target belongs to exactly one partition,
chosen by the CRC-32 of its name, so the partitions of one TOTAL cover the
package exactly once and stay stable as tests are added. Subtests run with
their parent.
"""
import re
import subprocess
import sys
import zlib

NAME = re.compile(r"^(Test|Example|Fuzz)\w*$")


def main():
    if len(sys.argv) < 3:
        raise SystemExit(__doc__)
    package, shard, flags = sys.argv[1], sys.argv[2], sys.argv[3:]
    match = re.fullmatch(r"([1-9][0-9]*)/([1-9][0-9]*)", shard)
    if not match or int(match[1]) > int(match[2]):
        raise SystemExit(f"Invalid shard {shard!r}; use INDEX/TOTAL with 1 <= INDEX <= TOTAL")
    index, total = int(match[1]) - 1, int(match[2])
    listed = subprocess.run(["go", "test", "-list", ".", package], check=True, capture_output=True, text=True).stdout
    names = sorted(line for line in listed.splitlines() if NAME.match(line))
    if not names:
        raise SystemExit(f"No tests discovered in {package}")
    selected = [name for name in names if zlib.crc32(name.encode()) % total == index]
    print(f"{package} shard {shard}: {len(selected)} of {len(names)} tests", flush=True)
    if not selected:
        return
    command = ["go", "test", package, "-run", "^(" + "|".join(selected) + ")$", *flags]
    raise SystemExit(subprocess.run(command).returncode)


if __name__ == "__main__":
    main()
