"""The config.json model: a small JSON Schema subset, defaults and the settings snapshot.

This deliberately implements only the keywords config.schema.json uses. It is not a
general JSON Schema engine; a test fails if the schema uses any other keyword.
"""
import copy
import json
import os
import re

MODES = ("all", "core-only", "web-only")
SERVICES = {"all": ("core", "web", "database"), "core-only": ("core", "database"), "web-only": ("web",)}
KEYWORDS = {"$schema", "title", "type", "enum", "const", "default", "description", "minimum", "maximum",
            "pattern", "items", "minItems", "uniqueItems", "properties", "required",
            "additionalProperties", "x-oac"}
ANNOTATIONS = {"changeable", "modes", "restarts", "native_restarts", "sensitive", "derives", "install_flag", "check",
               "setting"}


def _schema_text():
    # The loader reads from a directory or from inside the oac zipapp.
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.schema.json")
    return __loader__.get_data(path).decode("utf-8")


SCHEMA_TEXT = _schema_text()
SCHEMA = json.loads(SCHEMA_TEXT)


class ConfigError(Exception):
    """Every problem found in config.json, each naming its key. Values are never included."""

    def __init__(self, problems):
        self.problems = list(problems)
        super().__init__("config.json is not valid:\n" + "\n".join("  - " + item for item in self.problems))


def annotation(node, name, default=None):
    return node.get("x-oac", {}).get(name, default)


def leaves(node=None, prefix="", modes=MODES):
    """Yield (dotted key, schema node, modes) for every leaf, in schema order."""
    node = SCHEMA if node is None else node
    for name, child in node["properties"].items():
        key = prefix + name
        child_modes = tuple(annotation(child, "modes", modes))
        if "properties" in child:
            yield from leaves(child, key + ".", child_modes)
        else:
            yield key, child, child_modes


def lookup(config, key):
    value = config
    for part in key.split("."):
        if not isinstance(value, dict) or part not in value:
            return None
        value = value[part]
    return value


# Checks named by x-oac.check. Core stays the authority for its own semantic rules.
def _origin(value, https_only=False):
    """Core's ValidateSandboxCoreURL rule, through the installer's one implementation of it."""
    from configuration import valid_core_origin  # configuration imports this module at load time
    return valid_core_origin(value) and (not https_only or value.startswith("https://"))


_DURATION_UNITS = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "μs": 1e-6, "ms": 1e-3, "s": 1, "m": 60, "h": 3600}
_DURATION_PART = re.compile(r"([0-9]*(?:\.[0-9]*)?)(ns|us|µs|μs|ms|s|m|h)")


def duration_seconds(text):
    """Seconds in a Go duration string, or None when Go would reject it."""
    body = text[1:] if text[:1] in "+-" else text
    if body == "0":
        return 0.0
    total, position = 0.0, 0
    while position < len(body):
        part = _DURATION_PART.match(body, position)
        if not part or part[1] in ("", "."):
            return None
        total += float(part[1]) * _DURATION_UNITS[part[2]]
        position = part.end()
    if not body:
        return None
    return -total if text.startswith("-") else total


def _listen_host(value):
    from configuration import valid_listen_host
    return valid_listen_host(value)


CHECKS = {
    "listen_host": (_listen_host, "must be an IPv4 or IPv6 address without a port or zone"),
    "origin": (_origin, "must be a canonical origin such as https://core.example: lowercase, no path or "
                        "trailing slash, and HTTP only for a loopback host"),
    "https_origin": (lambda value: _origin(value, https_only=True), "must be a canonical HTTPS origin"),
    "go_duration": (lambda value: duration_seconds(value) is not None, "must be a Go duration such as 30m or 1h"),
    "go_duration_min_1h": (lambda value: (duration_seconds(value) or 0) >= 3600,
                           "must be a Go duration of at least 1h"),
}


def _type_ok(value, name):
    if name == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    return isinstance(value, {"string": str, "boolean": bool, "object": dict, "array": list,
                              "null": type(None)}[name])


def _validate(node, value, key, mode, problems):
    label = key or "config.json"
    types = node.get("type")
    if types is not None:
        types = [types] if isinstance(types, str) else types
        if not any(_type_ok(value, name) for name in types):
            problems.append(f"{label}: must be {' or '.join(types)}")
            return
    if "const" in node and (value != node["const"] or type(value) is not type(node["const"])):
        problems.append(f"{label}: must be {json.dumps(node['const'])}")
        return
    if "enum" in node and value not in node["enum"]:
        problems.append(f"{label}: must be one of {', '.join(json.dumps(item) for item in node['enum'])}")
        return
    if _type_ok(value, "integer"):
        if "minimum" in node and value < node["minimum"]:
            problems.append(f"{label}: must be at least {node['minimum']}")
        if "maximum" in node and value > node["maximum"]:
            problems.append(f"{label}: must be at most {node['maximum']}")
    if isinstance(value, str) and "pattern" in node and not re.search(node["pattern"], value):
        problems.append(f"{label}: has an invalid format")
    check = annotation(node, "check")
    if check and isinstance(value, str) and not CHECKS[check][0](value):
        problems.append(f"{label}: {CHECKS[check][1]}")
    if isinstance(value, list):
        if len(value) < node.get("minItems", 0):
            problems.append(f"{label}: needs at least {node['minItems']} item(s)")
        if node.get("uniqueItems") and len({json.dumps(item, sort_keys=True) for item in value}) != len(value):
            problems.append(f"{label}: lists an item twice")
        for index, item in enumerate(value):
            _validate(node.get("items", {}), item, f"{label}[{index}]", mode, problems)
    if isinstance(value, dict):
        properties = node.get("properties", {})
        for name in node.get("required", []):
            if name not in value:
                problems.append(f"{key + '.' if key else ''}{name}: is required")
        for name, item in value.items():
            child = f"{key}.{name}" if key else name
            if name in properties:
                if mode not in annotation(properties[name], "modes", MODES):
                    problems.append(f'{child}: does not apply when mode is "{mode}"; remove it')
                else:
                    _validate(properties[name], item, child, mode, problems)
            elif isinstance(node.get("additionalProperties"), dict):
                _validate(node["additionalProperties"], item, child, mode, problems)
            else:
                problems.append(f"{child}: unknown key")


def _complete(node, value, mode):
    for name, child in node.get("properties", {}).items():
        if mode not in annotation(child, "modes", MODES) or not annotation(child, "setting", True):
            continue
        if name not in value:
            if "default" in child:
                value[name] = copy.deepcopy(child["default"])
            elif "properties" in child:
                value[name] = {}
            else:
                continue
        if isinstance(value[name], dict) and "properties" in child:
            _complete(child, value[name], mode)


def validate(config):
    """Return config with defaults filled in for every applicable key, or raise ConfigError."""
    if not isinstance(config, dict):
        raise ConfigError(["config.json: must be a JSON object"])
    mode = config.get("mode")
    if mode not in MODES:
        raise ConfigError([f"mode: must be one of {', '.join(MODES)}"])
    problems = []
    _validate(SCHEMA, config, "", mode, problems)
    if problems:
        raise ConfigError(problems)
    full = copy.deepcopy(config)
    _complete(SCHEMA, full, mode)
    native = full.get("native_core", False)
    ports = full.get("ports", {})
    if mode != "web-only" and native != ("database" in ports):
        problems.append("ports.database: required exactly when native_core is true"
                        if native else "ports.database: applies only with native_core; remove it")
    if mode == "web-only" and "core_url" not in full.get("web", {}):
        problems.append("web.core_url: required for a web-only installation")
    if len(set(ports.values())) != len(ports):
        problems.append("ports: " + ", ".join(sorted(ports)) + " need different ports")
    from configuration import loopback_listener
    if not loopback_listener(full["host"]) and not (full["public_url"] or "").startswith("https://"):
        problems.append("public_url: an HTTPS origin is required when host is not loopback")
    core = full.get("core")
    if core and core["default_harness"] not in core["harnesses"]:
        problems.append("core.default_harness: must be listed in core.harnesses")
    if problems:
        raise ConfigError(problems)
    return ordered(full)


def ordered(config, node=None):
    """The same content with keys in schema order, so written files read like the reference."""
    node = SCHEMA if node is None else node
    if not isinstance(config, dict) or "properties" not in node:
        return config
    result = {}
    for name, child in node["properties"].items():
        if name in config:
            result[name] = ordered(config[name], child)
    for name, value in config.items():
        result.setdefault(name, value)
    return result


def initial(mode, native_core=False, **values):
    """Every applicable field for a new installation, seeded from installer flags."""
    config = {"$schema": "generated/config.schema.json", "format": 1, "mode": mode}
    if mode != "web-only":
        config["native_core"] = native_core
    for key, value in values.items():
        if value is None:
            continue
        target = config
        *parents, last = key.split(".")
        for part in parents:
            target = target.setdefault(part, {})
        target[last] = value
    return validate(config)


def is_setting(key, node, modes, config):
    return (config["mode"] in modes and annotation(node, "setting", True)
            and (key != "ports.database" or config.get("native_core", False)))


def values(config):
    """Every applicable setting of a validated config, by dotted key."""
    return {key: lookup(config, key) for key, node, modes in leaves() if is_setting(key, node, modes, config)}


def settings(config):
    """The non-secret snapshot Core serves at GET /core/v1/installation."""
    mode = config["mode"]
    items = []
    for key, node, modes in leaves():
        if not is_setting(key, node, modes, config):
            continue
        value = lookup(config, key)
        sensitive = annotation(node, "sensitive", False)
        item = {"key": key, "value": None if sensitive else value}
        if sensitive:
            item["configured"] = bool(value)
        # With native Core some settings restart more; native_restarts names them all.
        restarts = annotation(node, "native_restarts" if config.get("native_core") and
                              annotation(node, "native_restarts") else "restarts", [])
        item.update({"default": node.get("default"), "changeable": annotation(node, "changeable", True),
                     "sensitive": sensitive,
                     "restarts": [name for name in restarts if name in SERVICES[mode]]})
        items.append(item)
    return items


def sensitive_keys():
    return [key for key, node, _ in leaves() if annotation(node, "sensitive", False)]
