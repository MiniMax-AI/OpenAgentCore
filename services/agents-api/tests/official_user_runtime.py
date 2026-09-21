"""Public acceptance for an already enrolled, caller-owned Runtime.

run_acceptance must run on the main thread of the Linux operator process.
The supplied Session is unused and its /workspace/outputs directory is empty.
The operator creates nonempty .user-runtime-isolation-canary files under
/home/runtime/.parsar/parsar-daemon and /environment/staging before calling.
The actual executor-key.json must remain under that protected daemon root.
runtime.read(path) returns bytes within a bounded timeout, raising FileNotFoundError only for absence.
runtime.restart() preserves workspace and native state and waits for reconnect.
runtime.restart_core() preserves database and API URL and waits for reconnect.
The caller owns service/model configuration, Runtime cleanup and actual execution.
"""

import base64
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
import hashlib
import importlib.metadata
import json
from pathlib import Path
import signal
import threading
import time
import uuid

from openai import NotFoundError
from official_environment_files import verify_environment_files, verify_file_tenant_isolation
from official_session_artifacts import verify_session_artifacts


@contextmanager
def _prompt_deadline():
    # SSE keepalives do not extend the operator's wall-clock deadline.
    def expired(signum, frame):
        raise TimeoutError("Native Turn exceeded 240 seconds")

    previous = signal.signal(signal.SIGALRM, expired)
    signal.setitimer(signal.ITIMER_REAL, 240)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)


def run_acceptance(client, foreign, http, session, runtime, evidence_path, secret_markers=()):
    """Return a safe report after real execution; raise on any failed assertion."""
    assert threading.current_thread() is threading.main_thread(), "Run acceptance on the main thread"
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    source = json.loads(distribution.read_text("direct_url.json") or "{}")
    assert distribution.version == pin["sdk_version"] and source.get("vcs_info", {}).get("commit_id") == pin["commit"], "Install the pinned SDK"
    secrets = tuple(value.decode() if isinstance(value, bytes) else value
                    for value in (client.api_key, foreign.api_key, *secret_markers) if value)
    assert all(isinstance(value, str) for value in secrets), "Secret markers must be strings or UTF-8 bytes"
    evidence = Path(evidence_path)
    evidence.mkdir(mode=0o700, parents=True, exist_ok=True)
    sessions = client.beta.agents.sessions
    sid, eid = session.id, session.environment.id
    base = str(client.base_url).rstrip("/")
    endpoint = base + "/agents/sessions/" + sid
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    other_headers = {**headers, "Authorization": "Bearer " + foreign.api_key}
    report = {"passed": False, "session_id": sid, "environment_id": eid,
              "sdk_version": pin["sdk_version"], "sdk_commit": pin["commit"], "checks": [], "turns": []}
    began = time.monotonic()
    nonce = uuid.uuid4().hex
    prefix = "/workspace/user-runtime-" + nonce
    starts, ticks = prefix + "-starts", prefix + "-ticks"
    memory = "remember-" + uuid.uuid4().hex
    outputs = {"/workspace/outputs/a.bin": bytes(range(256)),
               "/workspace/outputs/b.txt": ("native-user-runtime-" + nonce + "\n").encode()}
    artifacts = {}
    private_paths = ["/home/runtime/.parsar/parsar-daemon/executor-key.json",
                     "/home/runtime/.parsar/parsar-daemon/.user-runtime-isolation-canary",
                     "/environment/staging/.user-runtime-isolation-canary"]

    def private_hashes():
        values = {}
        for path in private_paths:
            content = runtime.read(path)
            assert content, "Operator private-file witness is missing or empty"
            values[path] = hashlib.sha256(content).hexdigest()
        return values

    def redact(value):
        if isinstance(value, str):
            for secret in secrets:
                value = value.replace(secret, "[REDACTED]")
            return value
        if isinstance(value, dict):
            return {redact(key): redact(item) for key, item in value.items()}
        if isinstance(value, list):
            return [redact(item) for item in value]
        return value

    def save(name, value):
        path = evidence / name
        with path.open("w") as stream:
            path.chmod(0o600)
            json.dump(redact(value), stream, indent=2)

    def safe(value):
        assert redact(value) == value, "Public response exposed a private credential"
        return value

    def until(check, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = check()
            if value:
                return value
            time.sleep(0.2)
        raise AssertionError("Runtime observation timed out")

    def read_optional(path):
        try:
            return runtime.read(path)
        except FileNotFoundError:
            return b""

    def upload(path, data):
        content = data.encode() if isinstance(data, str) else data
        receipt = client.beta.agents.environments.files.create(
            eid, type="inline", path=path, data=base64.b64encode(content).decode()).to_dict()
        assert safe(receipt) == {"environment_id": eid, "object": "agent.environment.file",
                                 "path": path, "size_bytes": len(content)}, "Upload metadata changed"
        assert runtime.read(path) == content, "Public upload bytes differ in Runtime"

    def snapshot_items(name):
        items = [item.to_dict() for item in sessions.items.list(sid, order="asc", limit=100)]
        raw, after = [], None
        while True:
            params = {"order": "asc", "limit": 2}
            if after:
                params["after"] = after
            response = http.get(endpoint + "/items", headers=headers, params=params)
            assert response.status_code == 200, "Raw Items.list failed"
            page = safe(response.json())
            raw.extend(page["data"])
            if not page["has_more"]:
                break
            assert page["data"] and page["data"][-1]["id"] != after, "Items cursor did not advance"
            after = page["data"][-1]["id"]
            assert len(raw) <= len(items), "Raw Items.list exceeded SDK result"
        assert safe(items) == raw, "SDK and raw Items differ"
        save(name + "-items.json", items)
        return items

    def run_turn(text, number, cancellation=False):
        observed, stop, requested = [], threading.Event(), threading.Event()
        cancel_key = nonce + "-cancel"

        def cancel_running():
            deadline = time.monotonic() + 120
            while not stop.wait(0.2):
                assert time.monotonic() < deadline, "Native command never began ticking"
                current = read_optional(ticks)
                if len(current.splitlines()) >= 3:
                    assert runtime.read(starts) == b"started\n", "Side-effect command was repeated"
                    requested.set()
                    client.with_options(timeout=10).beta.agents.sessions.events.create(
                        sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key=cancel_key)
                    return len(current.splitlines())
            raise AssertionError("Cancellation ended before observing native effects")

        def consume(stream, journal, cancelling=None):
            for event in stream:
                value = event.to_dict()
                journal.write(json.dumps(redact(value)) + "\n")
                journal.flush()
                safe(value)
                observed.append(value)
                kind = value["type"]
                assert kind not in ("error", "agent.session.failed", "agent.session.turn.failed"), "Native execution failed"
                if cancelling is not None and cancelling.done():
                    cancelling.result()
                if kind == "agent.session.turn.cancelled":
                    assert cancellation and requested.is_set(), "Unexpected cancelled Turn"
                    cancelling.result(timeout=10)
                    break
                assert kind != "agent.session.turn.completed" or not cancellation, "Long command completed before cancellation"
                if kind == "agent.session.idle" and any(item["type"] == "agent.session.turn.completed" for item in observed):
                    break
            terminal = "agent.session.turn.cancelled" if cancellation else "agent.session.turn.completed"
            kinds = [item["type"] for item in observed]
            assert kinds.count("agent.session.turn.created") == 1 and kinds.count(terminal) == 1, "Missing or duplicate Turn events"
            assert kinds.index("agent.session.turn.created") < kinds.index(terminal), "Turn event order changed"
            if not cancellation:
                assert kinds[-1] == "agent.session.idle", "Completed Turn did not become idle"

        path = evidence / ("turn-" + str(number) + "-events.jsonl")
        try:
            with _prompt_deadline(), path.open("w") as journal:
                path.chmod(0o600)
                with sessions.events.stream(sid, timeout=240) as stream:
                    sessions.events.create(sid, events=[{"type": "agent.session.input.message", "input": [
                        {"role": "user", "content": [{"type": "input_text", "text": text}]}]}], idempotency_key=nonce + "-turn-" + str(number))
                    if cancellation:
                        with ThreadPoolExecutor(max_workers=1) as pool:
                            cancelling = pool.submit(cancel_running)
                            try:
                                consume(stream, journal, cancelling)
                            finally:
                                stop.set()
                            report["ticks_before_cancel"] = cancelling.result()
                    else:
                        consume(stream, journal)
            turns = list(sessions.turns.list(sid, order="asc", limit=100))
            assert len(turns) == number, "Input created an unexpected number of Turns"
            turn = turns[-1]
            assert turn.status == ("cancelled" if cancellation else "completed"), "Persisted Turn differs from SSE"
            until(lambda: sessions.retrieve(sid).status == "idle")
            items = snapshot_items("turn-" + str(number))
            current = [item for item in items if item.get("turn_id") == turn.id]
            assert current, "Native Turn has no public Items"
            if not cancellation:
                assert all(item.get("status") not in ("failed", "incomplete", "in_progress") for item in current), "Completed Turn contains failed native work"
            report["turns"].append({"id": turn.id, "status": turn.status, "event_count": len(observed)})
            return turn, current
        except BaseException:
            # No model request is retried; an already started tool is cancelled for cleanup.
            try:
                client.with_options(timeout=10).beta.agents.sessions.events.create(
                    sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key=nonce + "-failure-cancel")
            except Exception:
                report["failure_cleanup_cancel_failed"] = True
            raise

    def answer(items):
        return "\n".join(part["text"] for item in items if item.get("type") == "message" and item.get("role") == "assistant"
                         for part in item.get("content", []) if part.get("type") == "output_text")

    def check_artifacts():
        values = verify_session_artifacts(client, foreign, http, sid, eid, artifacts)
        safe([item.to_dict() for item in values])

    try:
        assert session.environment.type == "self_hosted" and session.environment.workspace_directory == "/workspace"
        assert client.beta.agents.environments.retrieve(eid).status == "connected", "Runtime is not connected"
        assert not list(sessions.turns.list(sid)) and not list(sessions.items.list(sid)), "Use an unused Session"
        assert not list(sessions.artifacts.list(sid)), "Use a Session without existing Artifacts"
        report["private_file_sha256"] = private_hashes()
        isolation = ("from pathlib import Path\nimport hashlib,os\n"
                     f"protected={private_paths!r}\n"
                     f"secret_hashes={sorted({hashlib.sha256(value.encode()).hexdigest() for value in secrets})!r}\n"
                     "for path in protected:\n"
                     " try:\n  with open(path,'rb') as f: exposed=f.read(1)\n"
                     " except (FileNotFoundError,PermissionError): continue\n"
                     " assert not exposed, 'Protected private bytes are visible'\n"
                     "for key,value in os.environ.items():\n"
                     " candidates=[value,value.removeprefix('Bearer ')]\n"
                     " assert all(hashlib.sha256(candidate.encode()).hexdigest() not in secret_hashes for candidate in candidates), 'Private credential is visible in tool environment'\n"
                     "proof={'protected_bytes_unreadable':True,'known_credentials_absent_from_environment':True}\n")
        def isolation_call(phase):
            return (f"proof=runpy.run_path({prefix + '-isolation.py'!r})['proof']\n"
                    f"with Path({prefix + '-isolation-' + phase + '.json'!r}).open('x') as f: json.dump(proof,f)\n")
        publish = ("from pathlib import Path\nimport json,runpy\n" + isolation_call("first")
                   + f"p=Path({prefix + '-published'!r})\nwith p.open('ab') as f: f.write(b'published\\n')\n"
                   "root=Path('/workspace/outputs');root.mkdir(exist_ok=True)\nassert not any(root.iterdir())\n"
                   + "\n".join(f"with Path({path!r}).open('xb') as f: f.write({data!r})" for path, data in outputs.items()) + "\n")
        hold = ("from pathlib import Path\nimport os,time\n"
                f"with Path({starts!r}).open('ab') as f: f.write(b'started\\n');f.flush();os.fsync(f.fileno())\n"
                f"with Path({ticks!r}).open('ab', buffering=0) as f:\n"
                " while True:\n  f.write(b'tick\\n');os.fsync(f.fileno());time.sleep(.2)\n")
        recover = "from pathlib import Path\nimport json,runpy\n" + isolation_call("recovered")
        resume = f"from pathlib import Path\nwith Path({prefix + '-resumed'!r}).open('xb') as f: f.write(b'resumed\\n')\n"
        for suffix, script in (("-isolation.py", isolation), ("-publish.py", publish), ("-recover.py", recover),
                               ("-hold.py", hold), ("-resume.py", resume)):
            upload(prefix + suffix, script)
        report["checks"].append("public_files_create_exact_runtime_bytes")

        def isolation_proof(phase):
            proof = json.loads(runtime.read(prefix + "-isolation-" + phase + ".json"))
            assert proof == {"protected_bytes_unreadable": True, "known_credentials_absent_from_environment": True}, "Native isolation probe failed"
            assert private_hashes() == report["private_file_sha256"], "Private witnesses changed across native execution or restart"
            report["isolation_" + phase] = proof

        first, items = run_turn("Remember this exact conversation-only token, including the remember- prefix: " + memory + ". "
                               "Use your native shell tool to run exactly `python3 " + prefix + "-publish.py` once in the foreground. "
                               "Do not edit the script, delegate, retry the command, or repeat its side effects. Report any error without retrying.", 1)
        assert any(item.get("type") == "command_execution" and item.get("status") == "completed" and prefix + "-publish.py" in item.get("command", "") for item in items), "Missing real native command observation"
        isolation_proof("first")
        assert runtime.read(prefix + "-published") == b"published\n", "Publish command was repeated"
        for path, data in outputs.items():
            assert runtime.read(path) == data, "Native output bytes differ"
        pages, page = verify_environment_files(client, http, eid, "/workspace/outputs", {path: len(data) for path, data in outputs.items()})
        save("files-list.json", safe(pages))
        verify_file_tenant_isolation(client, foreign, http, eid, "/workspace/outputs", page, list(outputs))
        artifacts[first.id] = outputs
        check_artifacts()
        for suffix in ("", "/items", "/turns", "/events"):
            response = http.get(endpoint + suffix, headers=other_headers)
            assert response.status_code == 404, "Foreign tenant accessed Session data"
            safe(response.json())
        try:
            foreign.beta.agents.sessions.items.list(sid)
        except NotFoundError:
            pass
        else:
            raise AssertionError("Foreign tenant accessed SDK Items")
        committed = snapshot_items("before-restart")
        report["checks"].append("native_outputs_files_artifacts_items_and_tenant_isolation")
        runtime.restart()
        runtime.restart_core()
        assert client.beta.agents.environments.retrieve(eid).status == "connected", "Runtime did not reconnect"
        assert snapshot_items("after-restart") == committed, "Restart changed committed Items"
        second, items = run_turn("Run exactly `python3 " + prefix + "-recover.py` once with your native shell tool; do not edit or retry it. "
                                "Then return the entire exact conversation-only remember- token from our previous turn, including its prefix. "
                                "Do not look for the token in files or rerun any earlier command.", 2)
        assert any(item.get("type") == "command_execution" and item.get("status") == "completed" and prefix + "-recover.py" in item.get("command", "") for item in items), "Missing recovered native isolation command"
        isolation_proof("recovered")
        assert memory in answer(items), "Cold continuation lost native conversation history"
        assert runtime.read(prefix + "-published") == b"published\n", "Cold continuation repeated side effects"
        artifacts[second.id] = outputs
        check_artifacts()
        report["checks"].append("runtime_and_core_restart_preserve_history_and_outputs")
        third, items = run_turn("Use your native shell tool to run exactly `python3 " + prefix + "-hold.py` once and wait in the foreground. "
                                "It intentionally runs until cancelled. Do not background, delegate, retry, edit the script, or restart the command.", 3, cancellation=True)
        commands = [item for item in items if item.get("type") == "command_execution" and prefix + "-hold.py" in item.get("command", "")]
        assert len(commands) == 1 and commands[0].get("status") in ("failed", "incomplete"), "Cancelled command lost its native outcome"
        stopped = runtime.read(ticks)
        time.sleep(2)
        assert runtime.read(ticks) == stopped and runtime.read(starts) == b"started\n", "Effects continued after cancellation settled"
        sessions.events.create(sid, events=[{"type": "agent.session.input.cancel"}], idempotency_key=nonce + "-cancel")
        assert len(list(sessions.turns.list(sid))) == 3, "Duplicate cancellation created work"
        check_artifacts()
        fourth, items = run_turn("Continue after the cancelled command. Never restart it or rerun the publish script. "
                                 "Run exactly `python3 " + prefix + "-resume.py` once with your native shell tool, without retries. "
                                 "Then return the entire original conversation-only remember- token, including its prefix.", 4)
        assert any(item.get("type") == "command_execution" and item.get("status") == "completed" and prefix + "-resume.py" in item.get("command", "") for item in items), "Missing resumed native command observation"
        assert memory in answer(items) and runtime.read(prefix + "-resumed") == b"resumed\n", "Post-cancel continuation failed"
        assert runtime.read(starts) == b"started\n" and runtime.read(ticks) == stopped, "Continuation repeated cancelled effects"
        assert runtime.read(prefix + "-published") == b"published\n", "Continuation repeated publication"
        artifacts[fourth.id] = outputs
        check_artifacts()
        report["checks"].append("public_cancel_stops_ticks_duplicate_cancel_and_continuation_preserve_effect_counts")
        report.update(passed=True, memory_sha256=hashlib.sha256(memory.encode()).hexdigest(),
                      output_sha256={path: hashlib.sha256(data).hexdigest() for path, data in outputs.items()},
                      ticks_after_cancel=len(stopped.splitlines()))
        return report
    except BaseException as error:
        report["failure_type"] = type(error).__name__
        raise
    finally:
        report["elapsed_seconds"] = round(time.monotonic() - began, 3)
        save("result.json", report)
