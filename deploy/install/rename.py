"""Resumable Core-host rename. Historical names are inputs and retained backups only.

No new service uses an old volume: copy while PostgreSQL is stopped and retain the
source forever. state.json is the journal, carried by the atomic directory move.
"""
import json
import contextlib
import fcntl
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import uuid

import config_model
import configuration
import convert
import native_service
import node_spec
import oac_cli
import sandbox_setup

RenameError = convert.ConvertError


def defaults():
    return Path.home() / ".parsar/core", Path.home() / ".oac/core"


def choose_root(explicit):
    old, new = defaults()
    if explicit is not None and explicit != old:
        return explicit
    if (new / "state.json").is_file():
        state = json.loads(oac_cli.read_private(new / "state.json", "state.json"))
        if state.get("renamed_from"):
            return new
    return explicit or old


def read_state(root):
    state = json.loads(oac_cli.read_private(root / "state.json", "state.json"))
    if not isinstance(state, dict):
        raise RenameError("state.json must contain an installation record")
    return state


def destination(root):
    old, new = defaults()
    return new if root == old else root


def private_parent(target):
    parent = target.parent
    if parent.is_symlink() or parent.resolve() != parent:
        raise RenameError("The destination's parent must be canonical and not a symlink")
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if parent == Path.home() / ".oac":
        os.chmod(parent, 0o700)


def api(root, config, legacy, path):
    base = oac_cli.core_base(config)
    key = root / ("admin/core.key" if legacy else "secrets/core.key")
    status, raw = oac_cli.http(base + "/core/v1/" + path, oac_cli.bearer(key.read_text().strip()))
    if status != 200:
        raise RenameError(f"Core did not authorize or answer {path} (HTTP {status}); check its Core key and services")
    try:
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise ValueError()
        return value
    except ValueError:
        raise RenameError("Core returned an invalid response for " + path) from None


def projects(root, config, legacy):
    result, cursor = [], ""
    while True:
        page = api(root, config, legacy, "projects?limit=100" + ("&after=" + cursor if cursor else ""))
        rows = page.get("data")
        if not isinstance(rows, list) or any(not isinstance(row, dict) or not isinstance(row.get("id"), str) for row in rows):
            raise RenameError("Core returned an invalid Project list")
        ids = [row["id"] for row in rows]
        if any(not re.fullmatch(r"[A-Za-z0-9_-]+", item) or item in result for item in ids):
            raise RenameError("Core returned an invalid Project cursor")
        result += ids
        if not page.get("has_more"):
            return sorted(result)
        if not ids:
            raise RenameError("Core returned an empty Project page with more results")
        cursor = ids[-1]


def drained(root, config, state, legacy, problems):
    """Read every independent guard; report all available problems together."""
    deployment = None
    try:
        deployment = api(root, config, legacy, "sandbox/deployment")
        if deployment.get("installation_id") != state["installation_id"]:
            problems.append("Core reports a different installation ID")
        if deployment.get("provider"):
            if deployment.get("maintenance") is not True:
                problems.append("Enable maintenance on the Nodes page with the previous release")
            resources = deployment.get("resources") or {}
            for name in ("allocations", "pending"):
                if type(resources.get(name)) is not int or resources[name] != 0:
                    problems.append(f"{name}: {resources.get(name, 'unknown')}; archive hosted Sessions with the previous release until zero")
    except RenameError as error:
        problems.append(str(error))
    try:
        nodes = api(root, config, legacy, "sandbox/nodes").get("data")
        if not isinstance(nodes, list):
            problems.append("Core did not return its registered node list")
        elif nodes:
            problems.append(f"{len(nodes)} registered node(s): remove each on the Nodes page and run its previous-release uninstall command")
    except RenameError as error:
        problems.append(str(error))
    try:
        project_ids = projects(root, config, legacy)
    except RenameError as error:
        problems.append(str(error))
        project_ids = None
    return deployment, project_ids


class Probe:
    """Start only a stopped old Core's dependencies, and undo those starts on refusal."""
    def __init__(self, root, config, state, legacy, run, out):
        self.root, self.config, self.state = root, config, state
        self.run, self.out = run, out
        self.compose = root / ("compose.json" if legacy else "generated/compose.json")
        self.started, self.native_started = [], False

    def start(self):
        if self.state["mode"] == "web-only" or oac_cli.http(oac_cli.core_base(self.config) + "/healthz")[0] == 200:
            return
        self.out("Starting the old Core with its existing files to inspect the conversion preconditions.")
        result = self.run(["docker", "ps", "--filter", "label=com.docker.compose.project=" + self.state["project"],
                           "--format", '{{.Label "com.docker.compose.service"}}'], capture_output=True, text=True)
        running = result.stdout.split()
        native = native_service.is_native(self.state)
        # Explicit services and --no-deps avoid running even the old migration job.
        for name in (["database"] if native else ["database", "core"]):
            if name not in running:
                self.started.append(name)  # Include a start whose response is lost.
                self.run(["docker", "compose", "-f", str(self.compose), "up", "--detach", "--wait", "--no-recreate", "--no-deps", name])
        if native:
            unit = convert.legacy_unit_name(self.state)
            result = self.run(["systemctl", "--user", "is-active", "--quiet", unit], check=False,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if result.returncode:
                self.native_started = True
                self.run(["systemctl", "--user", "start", unit])
        if oac_cli.wait_status(oac_cli.core_base(self.config) + "/healthz") is None:
            raise RenameError("The old Core did not become healthy with its existing files")

    def restore(self):
        failures = []
        if self.native_started:
            try:
                self.run(["systemctl", "--user", "stop", convert.legacy_unit_name(self.state)])
            except (OSError, subprocess.SubprocessError):
                failures.append("native Core")
        if self.started:
            try:
                self.run(["docker", "compose", "-f", str(self.compose), "stop", *reversed(self.started)])
            except (OSError, subprocess.SubprocessError):
                failures.append("old " + ", ".join(self.started))
        return failures


def volume(run, name):
    result = run(["docker", "volume", "inspect", name], capture_output=True, text=True, check=False)
    if result.returncode:
        # Differentiate a missing name from a daemon failure before granting creation.
        listed = run(["docker", "volume", "ls", "--format", "{{.Name}}"], capture_output=True, text=True)
        if name in listed.stdout.splitlines():
            raise RenameError("Cannot inspect database volume " + name)
        return None
    try:
        info, = json.loads(result.stdout)
        if info.get("Name") != name:
            raise ValueError()
        return info
    except (ValueError, TypeError):
        raise RenameError("Invalid database volume inspection for " + name) from None


def source_volume(run, state):
    name = state["project"] + "_database"
    info = volume(run, name)
    labels = (info or {}).get("Labels") or {}
    if (not info or info.get("Driver") != "local" or info.get("Options") or
            labels.get("com.docker.compose.project") != state["project"] or
            labels.get("com.docker.compose.volume") != "database"):
        raise RenameError("The source database volume is missing or is not this installation's local Compose volume")
    return name


def target_containers(run, project, labels=None):
    ids = run(["docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=" + project],
              capture_output=True, text=True).stdout.split()
    # Compose's default names can collide even when the existing container has
    # different labels. Include those names before any old service is stopped.
    named = run(["docker", "ps", "-aq", "--filter", "name=^/" + project + "-(database|core|migrate|web)-[0-9]+$"],
                capture_output=True, text=True).stdout.split()
    ids = sorted(set(ids + named))
    if ids and labels is None:
        raise RenameError("The target Compose project already has containers without a conversion journal")
    if ids:
        result = run(["docker", "inspect", "--format", "{{json .Config.Labels}}", *ids], capture_output=True, text=True)
        try:
            inspected = [json.loads(line) for line in result.stdout.splitlines()]
            if len(inspected) != len(ids) or any(not isinstance(item, dict) or
                    any(item.get(key) != value for key, value in labels.items()) or
                    item.get("com.docker.compose.project") != project or
                    item.get("com.docker.compose.service") != "database" for item in inspected):
                raise ValueError()
        except ValueError:
            raise RenameError("Refusing to remove an unowned container in the target Compose project") from None


def room(run, state):
    name = source_volume(run, state)
    # Both volumes use the same default local Docker filesystem. Read-only mount,
    # no network, and the already-installed database image; never pull an image.
    result = run(["docker", "run", "--rm", "--pull", "never", "--network", "none", "--user", "0:0",
                  "--entrypoint", "/bin/sh", "-v", name + ":/from:ro", state["images"]["database"],
                  "-c", "du -sk /from; df -Pk /from"], capture_output=True, text=True)
    try:
        lines = result.stdout.splitlines()
        used, available = int(lines[0].split()[0]), int(lines[-1].split()[3])
        if available < used:
            raise RenameError("Docker needs room for one more database volume copy")
    except (IndexError, ValueError):
        raise RenameError("Cannot measure database volume size and free space") from None


def preflight(root, target, manifest, public_url, run):
    problems, legacy_plan = [], None
    legacy = (root / "installation.json").exists()
    if legacy:
        old = convert.detect(root)
        compose = json.loads((root / "compose.json").read_text())
        images = {("core" if name == "migrate" else name): service["image"] for name, service in compose["services"].items()}
        old, legacy_plan, _ = convert.preflight(root, manifest, images, public_url, run, inspect_deployment=False)
        problems += legacy_plan.problems
        config, state = legacy_plan.config, legacy_plan.state
        if state:
            state["source_commit"] = old["source_commit"]
    else:
        state = read_state(root)
        config = None
        try:
            oac_cli.check_directories(root)
            config = oac_cli.load_config(root)
            oac_cli.check_fixed(config, state)
            oac_cli.check_secrets(root, config, state)
            if public_url is not None:
                problems.append("--public-url settles only a legacy layout conflict; edit config.json after conversion")
            # Current rendering has renamed paths, so compare against recorded old digests only.
            names = set(state.get("generated") or {})
            disk = oac_cli.read_generated(root, names)
            rendered = type("Recorded", (), {"files": {}})()
            problems += [f"generated/{name} was edited; restore it before conversion" for name in oac_cli.edited_files(state, disk, rendered)]
            for path in (root / "generated").iterdir():
                if path.is_symlink():
                    problems.append("generated/ must not contain symlinks")
            if not (root / "generated/compose.json").is_file():
                problems.append("generated/compose.json is missing; restore it with the previous release")
        except (oac_cli.OacError, config_model.ConfigError) as error:
            problems.append(str(error))
        if state.get("format") != 1:
            problems.append("The source is not a pre-rename state.json format 1 installation")
    if state:
        if not re.fullmatch(r"parsar-[0-9a-f]{10}", state.get("project", "")):
            problems.append("The source does not have a pre-rename Compose project name")
        try:
            uuid.UUID(state["installation_id"])
        except (ValueError, KeyError, TypeError):
            problems.append("The source has an invalid installation ID")
    if target != root and (target.is_symlink() or target.exists() and (not target.is_dir() or any(target.iterdir()))):
        problems.append("The destination must be absent or empty: " + str(target))
    if target.parent.is_symlink() or target.parent.resolve() != target.parent:
        problems.append("The destination parent must be canonical and not a symlink")
    if state and not problems:
        try:
            target_containers(run, "oac-" + state["project"][7:])
        except RenameError as error:
            problems.append(str(error))
    if state and state["mode"] != "web-only" and not problems:
        try:
            room(run, state)
            target_volume = "oac-" + state["project"][7:] + "_database"
            if volume(run, target_volume) is not None:
                problems.append("The target volume already exists without a conversion journal: " + target_volume)
        except RenameError as error:
            problems.append(str(error))
    return state, config, legacy_plan, problems


def refuse(problems):
    raise RenameError("This installation cannot be converted yet:\n" + "\n".join("  - " + item for item in problems)
                      + "\nNo installation conversion was performed.")


def confirm(root, target, state, plan, yes, out):
    compose = root / ("compose.json" if plan else "generated/compose.json")
    out("Conversion upgrades this installation to OpenAgentCore. Database migrations cannot be undone. Back up first:")
    if state["mode"] != "web-only":
        out("  docker compose -f " + shlex.quote(str(compose)) + " exec -T database pg_dump -U agents_api agents_api > oac-backup.sql")
        out("The old database volume is retained as a backup: " + state["project"] + "_database")
    out("Installation directory: " + str(root) + (" -> " + str(target) if target != root else " (converted in place)"))
    if plan:
        for source, destination in plan.moves:
            out(f"Move {source} -> {destination}")
        for note in plan.notes:
            out("Note: " + note)
        for name in plan.leftovers:
            out("Left in place: " + name)
    out("Self-hosted executors keep running. Archived hosted Sessions cannot resume their old sandboxes.")
    if not (yes or sys.stdin.isatty() and input("Type yes to convert this installation: ").strip() == "yes"):
        raise RenameError("Conversion was not confirmed. No installation conversion was performed; rerun with --yes to skip the prompt")


def save(root, state):
    oac_cli.save_state(root, state)


def copy_volume(root, state, run):
    record = state["renamed_from"]
    if state["mode"] == "web-only":
        return
    old = dict(state, project=record["project"], images=record["images"])
    source = source_volume(run, old)
    project = "oac-" + record["project"][7:]
    target = project + "_database"
    labels = configuration.conversion_labels(state)
    if record["volume_copied"]:
        existing = volume(run, target)
        actual = (existing or {}).get("Labels") or {}
        if not existing or any(actual.get(key) != value for key, value in labels.items()):
            raise RenameError("The completed conversion volume is missing or no longer owned; preserve the old backup")
        return
    target_containers(run, project, labels)
    document = {"name": project, "services": {"database": {"image": old["images"]["database"], "labels": labels,
                  "volumes": ["database:/var/lib/postgresql/data"]}}, "volumes": {"database": {"labels": labels}}}
    with tempfile.TemporaryDirectory(prefix=".conversion-", dir=root) as temporary:
        path = Path(temporary) / "compose.json"
        oac_cli.create_private(path, json.dumps(document))
        command = ["docker", "compose", "-f", str(path)]
        existing = volume(run, target)
        if existing is not None:
            actual = existing.get("Labels") or {}
            if (any(actual.get(key) != value for key, value in labels.items()) or
                    actual.get("com.docker.compose.project") != project or actual.get("com.docker.compose.volume") != "database"):
                raise RenameError("Refusing to remove an unowned conversion volume: " + target)
            run([*command, "rm", "--stop", "--force", "database"])
            run(["docker", "volume", "rm", target])
        run([*command, "create", "database"])
        run(["docker", "run", "--rm", "--pull", "never", "--network", "none", "--user", "0:0", "--entrypoint", "/bin/sh",
             "-v", source + ":/from:ro", "-v", target + ":/to", old["images"]["database"], "-c", "cp -a /from/. /to/ && sync"])
        record["volume_copied"] = True
        save(root, state)
        run([*command, "rm", "--stop", "--force", "database"])


def stub(source, target):
    source.mkdir(mode=0o700, parents=True, exist_ok=True)
    moved = ", and this installation moved to " + str(target) if source != target else ""
    message = f"parsar was renamed to oac{moved}. Run {target / 'oac'} <command>."
    oac_cli.write_private(source / "parsar", "#!/usr/bin/env python3\nimport sys\nprint(" + repr(message) + ", file=sys.stderr)\nsys.exit(1)\n")
    os.chmod(source / "parsar", 0o700)


def replace_runtime(root, config, state, manifest):
    record = state["renamed_from"]
    if state["mode"] == "web-only":
        return
    core, key = oac_cli.core_base(config), configuration.read_core_key(root)
    current = sandbox_setup.request(core, key, "GET", "deployment")
    if current.get("installation_id") != state["installation_id"]:
        raise RenameError("The upgraded Core reports a different installation")
    before = record["deployment"]
    provider = before.get("provider")
    if current.get("provider") != provider:
        raise RenameError("The deployment provider changed during conversion; inspect it before continuing")
    if provider in ("docker", "microsandbox"):
        desired = {"provider": provider, "resources": before["specification"]["resources"], "runtime": node_spec.release(manifest)}
        specification = current.get("specification") or {}
        matches = all(specification.get(key) == desired[key] for key in ("resources", "runtime"))
        if not matches:
            if current.get("generation") != before["generation"] or current.get("maintenance") is not True:
                raise RenameError("The deployment changed during conversion; keep maintenance enabled and inspect it")
            current = sandbox_setup.request(core, key, "PUT", "deployment", dict(desired, expected_generation=current["generation"]))
        if current.get("maintenance"):
            current = sandbox_setup.request(core, key, "PATCH", "deployment/maintenance",
                                            {"expected_generation": current["generation"], "maintenance": False})
        record["deployment_generation"] = current["generation"]
    elif provider == "e2b":
        if current.get("maintenance") is not True:
            raise RenameError("E2B must stay in maintenance until its template is rebuilt")
        print("E2B remains in maintenance. Rebuild the template with this release's build-template.py, replace it in Web, then resume admission.")
    elif current.get("generation") != before.get("generation"):
        raise RenameError("The deployment generation changed during conversion")
    save(root, state)


def execute(root, target, state, plan, manifest, load_images, run, finish, bundle):
    record = state["renamed_from"]
    if record["source_bundle"] != manifest["source_commit"]:
        raise RenameError("Finish the conversion with bundle " + record["source_bundle"])
    if state["installation_id"] != record["installation_id"] or state["source_commit"] not in (record["source_commit"], record["source_bundle"]):
        raise RenameError("Installation or source identity differs from the recorded conversion")
    if state.get("format") == 1 and (state["project"] != record["project"] or state["images"] != record["images"]):
        raise RenameError("Source project or images differ from the recorded conversion")
    if not record.get("old_removed"):
        # Until the old project is removed it may have been restarted for rollback.
        # Recopy on a resumed checkpoint instead of trusting a potentially stale copy.
        if record["volume_copied"]:
            record["volume_copied"] = False
            save(root, state)
        old = dict(state, project=record["project"])
        compose = root / ("compose.json" if (root / "installation.json").exists() else "generated/compose.json")
        if native_service.is_native(old):
            convert.legacy_disable(old, run)
        run(["docker", "compose", "-f", str(compose), "stop"])
        copy_volume(root, state, run)
        run(["docker", "compose", "-f", str(compose), "down"])
        record["old_removed"] = True
        save(root, state)
    else:
        existing = run(["docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=" + record["project"]],
                       capture_output=True, text=True).stdout.split()
        if existing:
            raise RenameError("The old project has containers again; preserve both databases and inspect the rollback before resuming")
        copy_volume(root, state, run)  # Validate ownership again before starting the copied database.
    if (root / "installation.json").exists():
        if not (root / "config.json").exists():
            if plan is None:
                _, plan, _ = convert.preflight(root, manifest, record["images"], None, run, inspect_deployment=False)
                if plan.problems:
                    refuse(plan.problems)
                plan.config["public_url"] = record["public_url"]
            plan.state = state
            convert.write_layout(root, convert.detect(root), plan)
        else:
            convert.resume(root, state, print)
    if root != target:
        private_parent(target)
        os.rename(root, target)
        root = target
    # Every write after the atomic move is repeatable from the carried journal.
    for path in [root / "parsar", root / ".parsar.lock", root / "generated" / (record["project"] + "-core.service")]:
        path.unlink(missing_ok=True)
    stub(Path(record["install_dir"]), root)
    state.update(format=2, project="oac-" + record["project"][7:], source_commit=manifest["source_commit"])
    state["images"] = load_images(list(record["images"]))
    # Old generated digests authorize replacing the old render; missing old unit is harmless.
    save(root, state)
    finish(root, bundle, manifest)
    state = read_state(root)
    config = oac_cli.load_config(root)
    replace_runtime(root, config, state, manifest)
    if state["mode"] != "web-only" and projects(root, config, False) != record["projects"]:
        raise RenameError("Project identities differ after conversion; preserve both volumes and inspect the installation")
    oac_cli.status(root)
    state["renamed_from"]["finished"] = True
    save(root, state)
    print("Converted installation: " + str(root))
    print("Add nodes: in Web, open Nodes and choose Add node.")
    if state["mode"] != "web-only":
        print("Keep the old database volume until the converted installation and history are verified. Then remove only this backup: docker volume rm "
              + record["project"] + "_database")
    return root


def _convert_installation(root, bundle, manifest, load_images, public_url, yes, run, finish):
    state = read_state(root) if (root / "state.json").exists() else None
    if state and state.get("renamed_from"):
        record = state["renamed_from"]
        source, target = Path(record["install_dir"]), Path(record["target_dir"])
        if (not source.is_absolute() or not target.is_absolute() or source.resolve() != source or
                target.resolve() != target or destination(source) != target or root not in (source, target) or
                not re.fullmatch(r"parsar-[0-9a-f]{10}", record.get("project", ""))):
            raise RenameError("Invalid conversion journal paths or project; preserve the installation and inspect state.json")
        if record.get("finished"):
            raise RenameError(f"Conversion already finished; run {root / 'oac'} status")
        if public_url is not None:
            if public_url != record["public_url"]:
                raise RenameError(f"This conversion already set public_url to {record['public_url']}; rerun without "
                                  "--public-url, and change it in config.json after the conversion")
            # Volume-copy checkpoints precede the legacy layout's config.json.
            # Once the layout exists, it must still agree with the journal.
            if (root / "config.json").exists():
                convert.check_resumed_public_url(root, public_url)
        return execute(root, Path(record["target_dir"]), state, None, manifest, load_images, run, finish, bundle)
    target = destination(root)
    state, config, plan, problems = preflight(root, target, manifest, public_url, run)
    if problems:
        # A live, readable Core can still report drain blockers alongside static
        # problems. Never start services until the static checks have passed.
        if state and config and state["mode"] != "web-only":
            try:
                oac_cli.check_private(root / ("admin/core.key" if plan else "secrets/core.key"), "Core key")
                if oac_cli.http(oac_cli.core_base(config) + "/healthz")[0] == 200:
                    drained(root, config, state, bool(plan), problems)
            except oac_cli.OacError:
                pass
        refuse(problems)
    probe = Probe(root, config, state, bool(plan), run, print)
    try:
        probe.start()
        if plan:
            _, updated, _ = convert.preflight(root, manifest, state["images"], public_url, run)
            problems += updated.problems
            config, plan = updated.config, updated
        deployment, ids = (None, None) if state["mode"] == "web-only" else drained(root, config, state, bool(plan), problems)
        if problems:
            refuse(problems)
        confirm(root, target, state, plan, yes, print)
    except BaseException:
        failed = probe.restore()
        if failed:
            raise RenameError("No installation conversion was performed, but the preflight could not restore the prior stopped state of "
                              + ", ".join(failed) + ". Inspect the old services before retrying.") from None
        raise
    record = {"install_dir": str(root), "target_dir": str(target), "project": state["project"],
              "installation_id": state["installation_id"], "source_commit": state["source_commit"], "source_bundle": manifest["source_commit"], "at": oac_cli.now(),
              "volume_copied": False, "finished": False, "images": state["images"], "deployment": deployment, "projects": ids,
              "public_url": config["public_url"]}
    state["renamed_from"] = record
    save(root, state)
    return execute(root, target, state, plan, manifest, load_images, run, finish, bundle)


@contextlib.contextmanager
def conversion_lock(root):
    # Lock the existing directory inode without creating a file before confirmation.
    # The same lock survives the atomic move to the destination.
    descriptor = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    previous = None
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        path = root / ".parsar.lock"
        if path.exists():
            previous = os.open(path, os.O_RDWR | os.O_NOFOLLOW)
            fcntl.flock(previous, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    except BlockingIOError:
        raise RenameError("Another management or conversion command is running for this installation") from None
    finally:
        if previous is not None:
            os.close(previous)
        os.close(descriptor)


def convert_installation(root, bundle, manifest, load_images, public_url, yes, run, finish):
    with conversion_lock(root):
        return _convert_installation(root, bundle, manifest, load_images, public_url, yes, run, finish)
