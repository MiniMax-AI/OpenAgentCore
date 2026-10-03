"""install.sh writes .env and starts Compose without host Python."""
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALL = ROOT / "deploy/install.sh"


class InstallScriptTests(unittest.TestCase):
    def install(self, root, *args, compose_up=0, route="1.1.1.1 via 10.0.0.1 dev eth0 src 10.0.0.5 uid 0"):
        bin_dir = root / "bin"
        bin_dir.mkdir(exist_ok=True)
        log = root / "docker.log"
        self.write_executable(bin_dir / "docker", textwrap.dedent(f"""\
            #!/bin/sh
            printf '%s\\n' "$*" >> {log}
            if [ "$1" = compose ] && [ "$2" = version ]; then printf 'v2.29.1\\n'; exit 0; fi
            if [ "$1" = compose ] && [ "$2" = cp ]; then printf '#!/bin/sh\\necho oac_core_fixture\\n' > ./oac; chmod +x ./oac; exit 0; fi
            if [ "$1" = compose ] && [ "$2" = up ]; then exit {compose_up}; fi
            exit 0
            """))
        self.write_executable(bin_dir / "curl", textwrap.dedent("""\
            #!/bin/sh
            output=""
            while [ $# -gt 0 ]; do
              if [ "$1" = --output ]; then output="$2"; shift 2; continue; fi
              shift
            done
            printf 'fixture\\n' > "$output"
            """))
        self.write_executable(bin_dir / "sha256sum", "#!/bin/sh\nexit 0\n")
        self.write_executable(bin_dir / "ss", "#!/bin/sh\nexit 0\n")
        self.write_executable(bin_dir / "ip", f"#!/bin/sh\nprintf '%s\\n' '{route}'\n")
        env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"], HOME=str(root))
        completed = subprocess.run(["bash", str(INSTALL), "--install-dir", str(root / "oac"), *args],
                                   env=env, capture_output=True, text=True)
        return completed, log.read_text() if log.exists() else ""

    def test_a_failed_first_start_stops_and_removes_the_directory(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, recorded = self.install(root, "--host", "127.0.0.1", "--web-port", "59991",
                                               "--public-url", "https://core.example", compose_up=1)
            self.assertNotEqual(completed.returncode, 0, completed.stderr)
            self.assertFalse((root / "oac").exists(), "a failed first start must remove the directory")
            self.assertIn("compose pull", recorded)
            self.assertIn("compose up -d --wait", recorded)
            self.assertIn("compose logs", recorded, "a failed start must show the services' logs")

    def test_the_private_address_is_the_default_public_url(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertIn("OAC_PUBLIC_URL=http://10.0.0.5:8080\n", (root / "oac/.env").read_text())
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
                self.assertIn("OAC_PUBLIC_URL=http://localhost:59992\n", (root / "oac/.env").read_text())
                self.assertIn("Only this host can open the console", completed.stdout)

    def test_env_holds_only_the_installation_choices(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            completed, _ = self.install(root, "--public-url", "https://core.example")
            self.assertEqual(completed.returncode, 0, completed.stderr)
            env = dict(line.split("=", 1) for line in (root / "oac/.env").read_text().splitlines())
            self.assertEqual(env["COMPOSE_FILE"], "compose.yaml:ports.yaml")
            self.assertEqual(env["OAC_PUBLIC_URL"], "https://core.example")
            self.assertEqual(sorted(env), ["COMPOSE_FILE", "COMPOSE_PROJECT_NAME", "OAC_HOST", "OAC_INSTALL_DIR", "OAC_PUBLIC_URL", "OAC_WEB_PORT"])

    def test_help_does_not_need_docker(self):
        help_text = subprocess.run(["bash", str(INSTALL), "--help"], capture_output=True, text=True, check=True)
        self.assertIn("--web-port", help_text.stdout)
        self.assertNotIn("--external-proxy", help_text.stdout)

    def write_executable(self, path, text):
        path.write_text(text)
        path.chmod(path.stat().st_mode | stat.S_IEXEC)


if __name__ == "__main__":
    unittest.main()
