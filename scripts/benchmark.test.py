"""Checks the published performance tables against the records they come from."""
import itertools
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def report(records):
    command = [sys.executable, str(ROOT / "scripts/benchmark.py"), "report", str(ROOT / records)]
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout.splitlines()


class PublishedTablesTests(unittest.TestCase):
    def test_tables_match_records(self):
        first, second = report("docs/performance"), report("docs/performance/cold")
        for page in ("docs/performance.md", "docs/zh/performance.md"):
            lines = (ROOT / page).read_text().splitlines()
            start = lines.index(first[0])
            self.assertEqual(lines[start:start + len(first)], first, page)
            rest = lines[start + len(first):]
            rows = list(itertools.takewhile(lambda line: line.startswith("|"), rest[rest.index(first[0]):]))[2:]
            self.assertTrue(rows, page)
            for row in rows:
                self.assertIn(row, second, page)


if __name__ == "__main__":
    unittest.main()
