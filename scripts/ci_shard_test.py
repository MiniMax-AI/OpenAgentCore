"""Coverage and failure propagation checks for the backend test partitions."""
import contextlib
import importlib.util
import io
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("go_test_shard", Path(__file__).with_name("go-test-shard.py"))
shard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shard)


class ShardTests(unittest.TestCase):
    names = [f"TestCase{i}" for i in range(100)] + ["Example", "ExampleStore_read", "FuzzInput"]

    def run_shard(self, index, total, result=0, listing=None):
        output = "\n".join(self.names) if listing is None else listing
        with patch.object(shard.sys, "argv", ["go-test-shard.py", "fixture", f"{index}/{total}", "-count=1"]), \
             patch.object(shard.subprocess, "run", side_effect=[subprocess.CompletedProcess([], 0, output), subprocess.CompletedProcess([], result)]) as run, \
             contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(SystemExit) as raised:
                shard.main()
            return raised.exception.code, run.call_args_list

    def test_partitions_cover_every_target_exactly_once(self):
        seen = []
        for index in range(1, 4):
            code, calls = self.run_shard(index, 3)
            self.assertEqual(code, 0)
            command = calls[1].args[0]
            pattern = command[command.index("-run") + 1]
            selected = [name for name in self.names if re.fullmatch(pattern, name)]
            self.assertFalse(set(selected) & set(seen))
            seen.extend(selected)
            self.assertIn("-count=1", command)
        self.assertCountEqual(seen, self.names)

    def test_unsharded_run_keeps_all_targets(self):
        _, calls = self.run_shard(1, 1)
        pattern = calls[1].args[0][4]
        self.assertTrue(all(re.fullmatch(pattern, name) for name in self.names))

    def test_test_failure_propagates(self):
        self.assertEqual(self.run_shard(1, 1, result=1)[0], 1)

    def test_empty_discovery_fails(self):
        code, calls = self.run_shard(1, 1, listing="ok fixture 0.1s\n")
        self.assertIn("No tests discovered", code)
        self.assertEqual(len(calls), 1)

    def test_discovery_failure_propagates(self):
        with patch.object(shard.sys, "argv", ["go-test-shard.py", "fixture", "1/3"]), \
             patch.object(shard.subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["go", "test"])):
            with self.assertRaises(subprocess.CalledProcessError):
                shard.main()

    def test_invalid_partition_fails_before_running_go(self):
        for value in ["0/3", "4/3", "1/0", "bad"]:
            with self.subTest(value=value), patch.object(shard.sys, "argv", ["go-test-shard.py", "fixture", value]), \
                 patch.object(shard.subprocess, "run") as run:
                with self.assertRaises(SystemExit):
                    shard.main()
                run.assert_not_called()

    def test_full_core_gate_ignores_shard_override(self):
        root = Path(__file__).resolve().parents[1]
        result = subprocess.check_output(["make", "-n", "check-core", "OAC_CORE_STORE_SHARD=2/3"], cwd=root, text=True)
        self.assertRegex(result, r"go-test-shard.py \S+ 1/1 ")
