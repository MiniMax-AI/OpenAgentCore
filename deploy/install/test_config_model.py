"""config.json schema, validation and the generated files it derives."""
import json
from pathlib import Path
import tempfile
import unittest

import config_model
import configuration


def schemas(node):
    """Every schema object in the schema document."""
    yield node
    for child in node.get("properties", {}).values():
        yield from schemas(child)
    for key in ("items", "additionalProperties"):
        if isinstance(node.get(key), dict):
            yield from schemas(node[key])


class ConfigModelTests(unittest.TestCase):
    def test_schema_uses_only_the_supported_keyword_subset(self):
        for node in schemas(config_model.SCHEMA):
            self.assertLessEqual(set(node), config_model.KEYWORDS)
            self.assertLessEqual(set(node.get("x-oac", {})), config_model.ANNOTATIONS)
            if "properties" in node:
                self.assertIs(node.get("additionalProperties"), False)

    def test_new_config_lists_every_applicable_setting(self):
        expected = {
            "all": ["mode", "native_core", "public_url", "host", "ports.core", "ports.web", "log.level", "log.format",
                    "log.add_source", "core.execution_concurrency", "core.harnesses", "core.default_harness",
                    "core.write_audit_retention", "core.oauth_trusted_origins", "core.database_pool.max_conns",
                    "core.database_pool.min_conns", "core.database_pool.max_conn_lifetime",
                    "core.database_pool.max_conn_idle_time", "core.database_pool.health_check_period",
                    "core.runtime_history.transport", "core.runtime_history.endpoint",
                    "core.runtime_history.insecure", "core.runtime_history.headers",
                    "core.runtime_history.queue_capacity", "core.runtime_history.timeout_seconds",
                    "core.runtime_history.sample_interval_seconds"],
            "web-only": ["mode", "public_url", "host", "ports.web", "web.core_url", "log.level", "log.format", "log.add_source"],
        }
        self.assertEqual(list(config_model.values(config_model.initial("all"))), expected["all"] + ["ingress"])
        web = config_model.initial("web-only", **{"web.core_url": "https://core.example"})
        self.assertEqual(list(config_model.values(web)), expected["web-only"] + ["ingress"])
        self.assertEqual(config_model.initial("core-only", True, **{"ports.database": 15432})["ports"],
                         {"core": 8091, "database": 15432})
        native = config_model.initial("all", True, **{"ports.database": 15432})
        self.assertEqual(native["ports"], {"core": 8091, "web": 8080, "database": 15432})
        restarts = {item["key"]: item["restarts"] for item in config_model.settings(native)}
        self.assertEqual(restarts["ports.core"], ["core", "web"])
        self.assertEqual({item["key"]: item["restarts"] for item in config_model.settings(config_model.initial("all"))}
                         ["ports.core"], ["core"])

    def test_invalid_settings_name_their_key_without_their_value(self):
        base = {"format": 1, "mode": "all"}
        cases = [
            ({"public_url": "https://core.example/"}, "public_url: must be a canonical origin"),
            ({"public_url": "https://Core.example"}, "public_url: must be a canonical origin"),
            ({"public_url": "http://core.example"}, "public_url: must be a canonical origin"),
            *(({"public_url": origin}, "public_url: must be a canonical origin") for origin in (
                "https://a_b.example", "https://core.example.", "https://b\u00fccher.example",
                "https://core.example:0443", "https://core.example:", "http://[2001:db8::1]")),
            ({"surprise": 1}, "surprise: unknown key"),
            ({"web": {"core_url": "https://core.example"}}, "web: does not apply when mode is \"all\""),
            ({"ports": {"core": 8080}}, "ports: core, web need different ports"),
            ({"ports": {"web": 80}}, "ports.web: must be at least 1024"),
            ({"native_core": True}, "ports.database: required exactly when native_core is true"),
            ({"core": {"harnesses": ["mcode"]}}, "core.default_harness: must be listed in core.harnesses"),
            ({"core": {"write_audit_retention": "59m"}}, "core.write_audit_retention: must be a Go duration of at least 1h"),
            ({"core": {"runtime_history": {"headers": {"Authorization": 7}}}}, "core.runtime_history.headers.Authorization: must be string"),
            ({"mode": "web-only"}, "web.core_url: required for a web-only installation"),
            ({"format": True}, "format: must be 1"),
            ({"format": 1.0}, "format: must be 1"),
        ]
        for change, message in cases:
            with self.subTest(change=change), self.assertRaises(config_model.ConfigError) as raised:
                config_model.validate(dict(base, **change))
            self.assertIn(message, str(raised.exception))
            self.assertNotIn("Core.example", str(raised.exception))
        for origin in ("http://127.0.0.1:8080", "http://localhost:8080", "https://core.example:8443",
                       "https://[2001:db8::1]:8443", "http://[::1]:8080", "http://[::ffff:127.0.0.1]:8080"):
            config_model.validate(dict(base, public_url=origin))

    def test_generated_files_hold_no_secret_and_the_snapshot_hides_sensitive_values(self):
        base = Path.home() / ".oac/tests/config-model"
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as temporary:
            root = Path(temporary).resolve()
            (root / "secrets").mkdir()
            secrets = {"core.key": "k" * 64, "credential.key": "credential-secret", "database.password": "database-secret"}
            for name, value in secrets.items():
                (root / "secrets" / name).write_text(value)
            state = {"installation_id": "5b7c0f3e-0000-4000-8000-000000000000", "project": "oac-0123456789",
                     "uid": 1000, "gid": 1000, "mode": "all", "native_core": False,
                     "images": {name: "sha256:" + "1" * 64 for name in ("core", "web", "database")}}
            headers = {"Authorization": "Bearer export-secret"}
            for native in (False, True):
                with self.subTest(native=native):
                    config = config_model.initial("all", native, **{"ports.database": 15432 if native else None})
                    config["core"]["runtime_history"] = {"endpoint": "collector.example:4317", "headers": headers}
                    rendered = configuration.render(root, config, dict(state, native_core=native), "2026-09-25T00:00:00Z")
                    environment = configuration.read_environment(rendered.files["core.env"])
                    self.assertRegex(environment["OAC_DATABASE_URL"], r"^postgres://agents_api@[^:/]+:\d+/agents_api\?sslmode=disable$")
                    for name, text in rendered.files.items():
                        for secret in [*secrets.values(), "export-secret"]:
                            if name != "runtime-history.json":
                                self.assertNotIn(secret, text, name)
                    self.assertEqual(json.loads(rendered.files["runtime-history.json"])["headers"], headers)
                    snapshot = json.loads(rendered.files["settings.json"])
                    item = next(item for item in snapshot["settings"] if item["key"] == "core.runtime_history.headers")
                    self.assertEqual((item["value"], item["configured"], item["sensitive"]), (None, True, True))
                    self.assertEqual(snapshot["path"], str(root / "config.json"))
                    self.assertEqual(snapshot["apply_command"], f"{root / 'oac'} apply")
                    self.assertEqual("oac-0123456789-core.service" in rendered.files, native)


if __name__ == "__main__":
    unittest.main()
