"""install.sh writes .env and starts Compose without host Python."""
import os
import fcntl
import sys
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "deploy/install.sh"


class InstallScriptTests(unittest.TestCase):
    def install(self, root, *args, compose_up=0, docker_info=0, key_status=0, download_status=0, kill_download=False, kill_start=False, route="1.1.1.1 via 10.0.0.1 dev eth0 src 10.0.0.5 uid 0"):
        bin_dir = root / "bin"
        bin_dir.mkdir(exist_ok=True)
        log = root / "docker.log"
        self.write_executable(bin_dir / "docker", textwrap.dedent(f"""\
            #!/bin/sh
            printf '%s\\n' "$*" >> {log}
            if [ "$1" = info ]; then exit {docker_info}; fi
            if [ "$1" = compose ] && [ "$2" = logs ]; then echo service-diagnostic >&2; fi
            if [ "$1" = compose ] && [ "$2" = config ] && [ "$3" = --environment ]; then sed "s/'//g" .env; fi
            if [ "$1" = compose ] && [ "$2" = version ]; then printf 'v2.29.1\\n'; exit 0; fi
            if [ "$1" = compose ] && [ "$2" = cp ]; then printf '#!/bin/sh\\necho oac_core_fixture\\nexit {key_status}\\n' > ./oac.download; chmod +x ./oac.download; exit 0; fi
            if [ "$1" = compose ] && [ "$2" = up ]; then {'kill -KILL "$PPID"' if kill_start else ':'}; exit {compose_up}; fi
            exit 0
            """))
        self.write_executable(bin_dir / "curl", textwrap.dedent(f"""\
            #!/bin/sh
            exit_status={download_status}
            [ "$exit_status" = 0 ] || exit "$exit_status"
            output=""
            while [ $# -gt 0 ]; do
              if [ "$1" = --output ]; then output="$2"; shift 2; continue; fi
              shift
            done
            printf 'fixture\\n' > "$output"
            {'kill -KILL "$PPID"' if kill_download else ':'}
            """))
        self.write_executable(bin_dir / "sha256sum", "#!/bin/sh\nexit 0\n")
        self.write_executable(bin_dir / "uname", '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        self.write_executable(bin_dir / "flock", f'#!{sys.executable}\nimport fcntl, sys\ntry: fcntl.flock(int(sys.argv[-1]), fcntl.LOCK_EX | fcntl.LOCK_NB)\nexcept BlockingIOError: sys.exit(1)\n')
        self.write_executable(bin_dir / "ss", "#!/bin/sh\nexit 0\n")
        self.write_executable(bin_dir / "ip", f"#!/bin/sh\nprintf '%s\\n' '{route}'\n")
        env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"], HOME=str(root))
        completed = subprocess.run(["bash", str(INSTALL), "--install-dir", str(root / "oac"), *args],
                                   env=env, capture_output=True, text=True)
        return completed, log.read_text() if log.exists() else ""

    def test_a_failed_start_preserves_configuration_and_reports_service_logs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, recorded = self.install(root, "--host", "127.0.0.1", "--web-port", "59991",
                                               "--public-url", "https://core.example", compose_up=1)
            self.assertNotEqual(completed.returncode, 0, completed.stderr)
            self.assertTrue((root / "oac/.env").exists())
            self.assertNotIn("compose down", recorded)
            self.assertIn("service-diagnostic", completed.stderr)
            self.assertIn("data retained", completed.stderr)
            self.assertIn("compose pull", recorded)
            self.assertIn("compose up -d --wait", recorded)
            self.assertIn("compose logs", recorded, "a failed start must show the services' logs")

    def test_the_private_address_is_the_default_public_url(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertIn("OAC_PUBLIC_URL='http://10.0.0.5:8080'\n", (root / "oac/.env").read_text())
            self.assertIn("Console   http://10.0.0.5:8080", completed.stdout)
            self.assertIn("Core key  oac_core_fixture", completed.stdout)
            self.assertNotIn("Only this host", completed.stdout)

    def test_without_a_private_address_only_this_host_reaches_web(self):
        for args, route in ((["--web-port", "59992"], "1.1.1.1 dev eth0 src 203.0.113.5 uid 0"),
                            (["--host", "127.0.0.1", "--web-port", "59992"], "1.1.1.1 dev eth0 src 10.0.0.5 uid 0")):
            with self.subTest(args=args), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                completed, _ = self.install(root, *args, route=route)
                self.assertEqual(completed.returncode, 0, completed.stderr)
                self.assertIn("OAC_PUBLIC_URL='http://localhost:59992'\n", (root / "oac/.env").read_text())
                self.assertIn("Only this host can open the console", completed.stdout)

    def test_env_holds_only_the_installation_choices(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root, "--public-url", "https://core.example")
            self.assertEqual(completed.returncode, 0, completed.stderr)
            env = dict((lambda pair: (pair[0], pair[1].strip("'")))(line.split("=", 1)) for line in (root / "oac/.env").read_text().splitlines())
            self.assertEqual(env["OAC_PUBLIC_URL"], "https://core.example")
            self.assertEqual(sorted(env), ["COMPOSE_PROJECT_NAME", "OAC_HOST", "OAC_INSTALL_DIR", "OAC_PUBLIC_URL", "OAC_WEB_PORT"])

    def test_key_failure_never_removes_a_started_installation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, recorded = self.install(root, key_status=1)
            self.assertNotEqual(completed.returncode, 0)
            self.assertTrue((root / "oac/.env").exists())
            self.assertNotIn("compose down", recorded)
            self.assertIn("Core key could not be read", completed.stderr)

    def test_retry_reuses_saved_settings_and_keeps_data(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            failed, _ = self.install(root, "--public-url", "https://core.example", compose_up=1)
            self.assertNotEqual(failed.returncode, 0)
            configuration = (root / "oac/.env").read_bytes()
            data = root / "oac/data"
            data.mkdir()
            (data / "keep").write_text("user data")
            (root / "docker.log").unlink()
            completed, recorded = self.install(root, download_status=22)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertEqual((root / "oac/.env").read_bytes(), configuration)
            self.assertEqual((data / "keep").read_text(), "user data")
            self.assertIn("Console   https://core.example", completed.stdout)
            self.assertNotIn("compose pull", recorded)
            self.assertNotIn("Only this host", completed.stdout)

    def test_invalid_port_and_unavailable_docker_fail_before_installation(self):
        for args, status in [(["--web-port", "0"], 0), (["--web-port", "70000"], 0), ([], 1)]:
            with self.subTest(args=args, status=status), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                completed, recorded = self.install(root, *args, docker_info=status)
                self.assertNotEqual(completed.returncode, 0)
                self.assertFalse((root / "oac").exists())
                self.assertNotIn("compose pull", recorded)

    def test_failed_download_leaves_no_installation_or_staging(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, recorded = self.install(root, download_status=22)
            self.assertNotEqual(completed.returncode, 0)
            self.assertFalse((root / "oac").exists())
            self.assertFalse((root / "oac.staging").exists())
            self.assertNotIn("compose down", recorded)

    def test_retry_after_sigkill_cleans_unfinished_download(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            killed, _ = self.install(root, kill_download=True)
            self.assertEqual(killed.returncode, -9, killed.stderr)
            self.assertTrue((root / "oac.staging").exists())
            self.assertFalse((root / "oac").exists())
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertFalse((root / "oac.staging").exists())
            self.assertTrue((root / "oac/oac").exists())

    def test_retry_after_start_sigkill_cleans_published_log(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            killed, _ = self.install(root, kill_start=True)
            self.assertEqual(killed.returncode, -9, killed.stderr)
            self.assertTrue((root / "oac/install.log").exists())
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertFalse((root / "oac/install.log").exists())
            self.assertFalse((root / "oac.staging").exists())

    def test_retry_clears_staging_from_an_interrupted_process(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staging = root / "oac.staging"
            staging.mkdir()
            (staging / ".oac-installer").write_text(f"OpenAgentCore staging for {root / 'oac'}\n")
            (staging / "partial").write_text("unfinished download")
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertFalse(staging.exists())
            self.assertFalse((root / "oac/partial").exists())

    def test_unrecognized_staging_directory_is_never_deleted(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staging = root / "oac.staging"
            staging.mkdir()
            (staging / "keep").write_text("user data")
            completed, recorded = self.install(root)
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("Unrecognized staging", completed.stderr)
            self.assertEqual((staging / "keep").read_text(), "user data")
            self.assertNotIn("compose up", recorded)

    def test_existing_unrelated_directory_is_never_deleted(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "oac").mkdir()
            (root / "oac/keep").write_text("user data")
            completed, recorded = self.install(root)
            self.assertNotEqual(completed.returncode, 0)
            self.assertEqual((root / "oac/keep").read_text(), "user data")
            self.assertNotIn("compose down", recorded)

    def test_another_installation_holds_the_lock(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (root / "oac.install.lock").open("w") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                completed, recorded = self.install(root)
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("Another installation", completed.stderr)
            self.assertFalse((root / "oac").exists())
            self.assertNotIn("compose pull", recorded)

    def test_retry_does_not_overlap_an_oac_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            (root / "docker.log").unlink()
            with (root / "oac/.oac.lock").open("r+") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                completed, recorded = self.install(root)
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("Another oac command", completed.stderr)
            self.assertNotIn("compose up", recorded)

    def test_rerun_does_not_silently_replace_settings(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            before = (root / "oac/.env").read_bytes()
            completed, _ = self.install(root, "--web-port", "9000")
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertIn("Using saved settings", completed.stdout)
            self.assertIn("only apply to new directories", completed.stdout)
            self.assertEqual((root / "oac/.env").read_bytes(), before)

    def test_help_does_not_need_docker(self):
        help_text = subprocess.run(["bash", str(INSTALL), "--help"], capture_output=True, text=True, check=True)
        self.assertIn("--web-port", help_text.stdout)
        self.assertNotIn("--external-proxy", help_text.stdout)

    def write_executable(self, path, text):
        path.write_text(text)
        path.chmod(path.stat().st_mode | stat.S_IEXEC)


if __name__ == "__main__":
    unittest.main()
