#!/usr/bin/env python3
"""Real-model acceptance of the six pinned Subagent GET operations.

Install openai 3.13.0 from the commit in contracts/agents-api/upstream.json.
Required ENV: AGENTS_API_BASE_URL (including /v1), AGENTS_API_TOKEN, and
AGENTS_API_FOREIGN_TOKEN (a valid key for a different project). Creating a Session
also requires AGENTS_API_MODEL. AGENTS_API_ENVIRONMENT_JSON defaults to
{"type":"none"}; AGENTS_API_HARNESS optionally selects the existing Core extension.
AGENTS_API_SESSION_ID selects an existing Session. Close/resume use the native
conversation context from spawn; nullable names/instructions are not prerequisites.

Run --phase all for the nested/close/resume native profile, --phase spawn-direct
for common reads with two real children, or spawn, inspect, close, resume using
the same --evidence directory under ~/.parsar. Inspect never submits model input.
For an independently prepared Session, inspect requires two real children and
proves reads only, not the full lifecycle. Use --require-nested when the native
profile supports nested delegation; absence is recorded, not fabricated. All/full acceptance requires observed spawn, nested, close and resume
facts; model prose is never accepted as evidence of a native action.

Child work must appear only where the official service shows it. Inspection
records each of these visibility checks under "visibility" (per phase) and fails
only after running all of them, so a baseline run reports every difference at once:
Session Turn list/retrieve/cursor carry root Turns only and a child Turn ID
answers like a missing one (also for the foreign project); child Turns carry the
Session's Agent ID with their own subagent_id; every list uses the object/first_id/
last_id envelope with null IDs on an empty page; child Item lists clamp limit 0
and 101 while the Subagent and Subagent Turn lists reject them; and the Session
stream observed while this script submitted input (the creation stream, or the
GET stream for later input) carries no child Turn or Item event, and carries
agent.session.subagent.created when children were spawned.

Evidence contains only IDs, timestamps, counts and named checks. No HTTP bodies,
model output, API keys or provider configuration are written. Sessions are left
for the operator to inspect and clean up. This script starts no service/runtime.
"""

import argparse
import importlib.metadata
import json
import os
from pathlib import Path
import re
import sys
import threading
import time
from urllib.parse import quote, urlsplit
import uuid


class AcceptanceFailure(Exception):
    pass


class StreamObserver:
    """Summarizes Session events without retaining bodies. The reader is a daemon
    thread; stop() reads what was observed even if the live stream stays open."""

    def __init__(self, stream, kind):
        self.stream, self.kind = stream, kind
        self.events, self.session_id, self.error, self.ended, self.stopping = [], None, None, False, False
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def run(self):
        try:
            for event in self.stream:
                value = event.to_dict()
                if value.get("type") == "agent.session.created":
                    self.session_id = (value.get("session") or {}).get("id")
                turn, item = value.get("turn") or {}, value.get("item") or {}
                self.events.append({"type": value.get("type"), "turn_ids": {v for v in (
                    value.get("turn_id"), turn.get("id"), item.get("turn_id")) if v},
                    "child_turn": turn.get("subagent_id") is not None})
        except Exception as error:
            if not self.stopping:
                self.error = type(error).__name__
        finally:
            self.ended = True

    def stop(self):
        if self.kind == "creation":
            # The creation stream ends by itself once the Session settles.
            self.thread.join(60)
        if not self.ended:
            # A GET stream stays open; closing it ends the reader with a connection error.
            self.stopping = True
            self.stream.close()
            self.thread.join(5)
        return list(self.events)


def require(condition, code):
    if not condition:
        raise AcceptanceFailure(code)


def identifier(value):
    require(isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,200}", value), "invalid_resource_id")
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--phase", choices=("spawn", "spawn-direct", "inspect", "close", "resume", "all"), default="inspect")
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--require-nested", action="store_true", help="Require native nested delegation in an externally prepared fixture")
    args = parser.parse_args()
    require(1 <= args.timeout <= 1800, "invalid_timeout")
    evidence = args.evidence.expanduser().resolve()
    require(Path.home().joinpath(".parsar") in evidence.parents, "evidence_must_be_under_parsar_home")
    evidence.mkdir(parents=True, exist_ok=True, mode=0o700)
    path = evidence / "subagents-proof.json"
    report = json.loads(path.read_text()) if path.exists() else {
        "passed": False, "checks": [], "phases": {}, "nonce": uuid.uuid4().hex,
    }
    token = os.environ.get("AGENTS_API_TOKEN", "")
    foreign_token = os.environ.get("AGENTS_API_FOREIGN_TOKEN", "")

    def save():
        raw = json.dumps(report, indent=2) + "\n"
        require(not any(secret and secret in raw for secret in (token, foreign_token)), "secret_in_evidence")
        temporary = path.with_suffix(".tmp")
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(raw)
        os.replace(temporary, path)

    def checked(name):
        if name not in report["checks"]:
            report["checks"].append(name)

    client = foreign = http = None
    try:
        pin = json.loads((Path(__file__).resolve().parents[1] / "contracts/agents-api/upstream.json").read_text())
        distribution = importlib.metadata.distribution("openai")
        source = json.loads(distribution.read_text("direct_url.json") or "{}")
        require(distribution.version == pin["sdk_version"] == "3.13.0" and
                source.get("vcs_info", {}).get("commit_id") == pin["commit"], "pinned_sdk_required")
        report.update(sdk_version=pin["sdk_version"], sdk_commit=pin["commit"], passed=False)
        base = os.environ.get("AGENTS_API_BASE_URL", "").rstrip("/")
        parsed = urlsplit(base)
        require(parsed.scheme in ("http", "https") and parsed.netloc and parsed.path.endswith("/v1")
                and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment,
                "invalid_base_url")
        require(token and foreign_token and token != foreign_token, "distinct_project_tokens_required")
        import httpx2
        from openai import NotFoundError, OpenAI
        client = OpenAI(base_url=base, api_key=token, max_retries=0, timeout=30,
                        _strict_response_validation=True, http_client=httpx2.Client(trust_env=False))
        foreign = OpenAI(base_url=base, api_key=foreign_token, max_retries=0, timeout=30,
                         _strict_response_validation=True, http_client=httpx2.Client(trust_env=False))
        http = httpx2.Client(trust_env=False, timeout=30)
        # A bad credential cannot accidentally count as tenant-isolation proof.
        foreign.beta.agents.sessions.list(limit=1)
        sessions = client.beta.agents.sessions
        supplied = os.environ.get("AGENTS_API_SESSION_ID")
        if supplied:
            require(not report.get("session_id") or report["session_id"] == supplied, "evidence_session_mismatch")
            report["session_id"] = identifier(supplied)
        headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
        other_headers = {**headers, "Authorization": "Bearer " + foreign_token}

        def sid():
            return identifier(report["session_id"])

        def endpoint(suffix=""):
            return base + "/agents/sessions/" + quote(sid(), safe="") + suffix

        def raw(suffix, params=None, expected=200, other=False):
            response = http.get(endpoint(suffix), headers=other_headers if other else headers, params=params)
            require(response.status_code == expected, "raw_status_" + str(expected) + "_expected")
            body = response.json()
            if expected != 200:
                require(isinstance(body.get("error"), dict), "error_envelope_missing")
            return body

        def cursor_error(suffix, after):
            """An unresolved cursor is the family's official 400 (ERROR-PROTOCOL-001)."""
            message = "Invalid session item ID in `after`" if suffix.endswith("/items") else "Invalid resource ID in `after`"
            error = raw(suffix, {"after": after}, expected=400)["error"]
            require(error == {"message": message, "type": "invalid_request_error", "code": "invalid_request_error",
                              "param": None}, "cursor_error_mismatch")

        def sdk_list(resource, *pos, **keywords):
            values, seen = [], set()
            for item in resource.list(*pos, limit=100, order="asc", **keywords):
                require(item.id not in seen, "sdk_cursor_repeated_resource")
                seen.add(item.id)
                values.append(item.to_dict())
                require(len(values) <= 5000, "history_bound_exceeded")
            return values

        def all_turns():
            return sdk_list(sessions.turns, sid())

        def children():
            return sdk_list(sessions.subagents, sid())

        def wait_for(predicate, code, timeout=None):
            deadline = time.monotonic() + (args.timeout if timeout is None else timeout)
            while time.monotonic() < deadline:
                value = predicate()
                if value:
                    return value
                time.sleep(1)
            raise AcceptanceFailure(code)

        def root_finished(before):
            turns = [turn for turn in all_turns() if turn.get("subagent_id") is None and turn["id"] not in before]
            require(len(turns) <= 1, "concurrent_root_input_detected")
            if not turns:
                return False
            turn = turns[0]
            require(turn["status"] not in ("failed", "cancelled"), "root_execution_failed")
            return turn if turn["status"] == "completed" else False

        observers = {}

        def submit(stage, prompt):
            require(report["phases"].get(stage) != "passed", "phase_already_passed")
            require(not report.get("pending_phase"), "pending_phase_requires_operator_reconciliation")
            before = {turn["id"] for turn in all_turns() if turn.get("subagent_id") is None} if report.get("session_id") else set()
            report["pending_phase"] = stage
            save()
            if not report.get("session_id"):
                model = os.environ.get("AGENTS_API_MODEL")
                require(model, "model_required_for_creation")
                agent = {"model": model, "multi_agent": {"enabled": True, "max_concurrent_subagents": 4}}
                harness = os.environ.get("AGENTS_API_HARNESS")
                if harness:
                    agent["x_agents_core"] = {"harness": harness}
                environment = json.loads(os.environ.get("AGENTS_API_ENVIRONMENT_JSON", '{"type":"none"}'))
                # The creation stream is the Session stream observed for this phase.
                observer = StreamObserver(sessions.create(agent=agent, environment=environment, input=prompt, stream=True,
                                                          extra_headers={"Idempotency-Key": report["nonce"] + "-" + stage}), "creation")
                observers[stage] = observer
                wait_for(lambda: observer.session_id or observer.ended, "creation_stream_timeout", timeout=30)
                require(observer.session_id, "creation_stream_missing_created_event")
                report["session_id"] = identifier(observer.session_id)
                save()
            else:
                require(sessions.retrieve(sid()).agent.multi_agent.enabled, "existing_session_delegation_disabled")
                # Open the live stream before submitting, as the stream is live-only.
                observers[stage] = StreamObserver(sessions.events.stream(sid()), "events")
                sessions.events.create(sid(), events=[{"type": "agent.session.input.message", "input": [
                    {"role": "user", "content": [{"type": "input_text", "text": prompt}]}]}],
                    idempotency_key=report["nonce"] + "-" + stage)
            turn = wait_for(lambda: root_finished(before), "root_turn_timeout")
            report.setdefault("root_turns", {})[stage] = identifier(turn["id"])
            report.pop("pending_phase", None)
            save()

        def page_check(suffix, expected):
            """Pagination and scope checks that predate the visibility batch."""
            expected_ids = [identifier(item["id"]) for item in expected]
            require(len(expected_ids) == len(set(expected_ids)), "duplicate_resource_id")
            for order in ("asc", "desc"):
                for limit in (1, 2):
                    observed, after = [], None
                    while True:
                        params = {"order": order, "limit": limit}
                        if after:
                            params["after"] = after
                        page = raw(suffix, params)
                        require(isinstance(page.get("data"), list) and isinstance(page.get("has_more"), bool), "invalid_page_shape")
                        require(len(page["data"]) <= limit, "page_limit_not_enforced")
                        observed.extend(page["data"])
                        require(len(observed) <= len(expected), "pagination_duplicate_or_unstable_history")
                        if not page["has_more"]:
                            break
                        require(page["data"] and page["data"][-1]["id"] != after, "cursor_did_not_advance")
                        after = page["data"][-1]["id"]
                    require(observed == (expected if order == "asc" else list(reversed(expected))), "sdk_raw_or_pagination_mismatch")
            defaults = raw(suffix)
            require(defaults["data"] == list(reversed(expected))[:20], "pagination_defaults_mismatch")
            # Unknown list keys are ignored (list query tolerance, row A1).
            require(raw(suffix, {"unknown": "value"})["data"] == defaults["data"], "unknown_list_key_not_ignored")
            cursor_error(suffix, str(uuid.uuid4()))
            raw(suffix, {"order": "invalid"}, expected=400)
            raw(suffix, other=True, expected=404)

        visibility = {}

        def visible(name, check):
            """Runs one visibility check to completion and records its result."""
            try:
                check()
                result = "passed"
            except AcceptanceFailure as error:
                result = str(error)
            # A check repeated per resource keeps its first failure.
            if visibility.get(name, "passed") == "passed":
                visibility[name] = result

        def envelope_check(suffix, expected, clamped):
            ids = [item["id"] for item in expected]
            window = ids[:100]
            first = raw(suffix, {"order": "asc", "limit": 100})
            require(first.get("object") == "list" and first.get("first_id") == (window[0] if window else None) and
                    first.get("last_id") == (window[-1] if window else None), "list_envelope_mismatch")
            if ids:
                empty = raw(suffix, {"order": "asc", "after": ids[-1]})
                require(empty == {"object": "list", "data": [], "first_id": None, "last_id": None, "has_more": False},
                        "empty_page_envelope_mismatch")
            for limit, size in ((0, 1), (101, 100)):
                if clamped:
                    page = raw(suffix, {"order": "asc", "limit": limit})
                    require([item["id"] for item in page["data"]] == ids[:size] and page["has_more"] == (len(ids) > size),
                            "limit_" + str(limit) + "_not_clamped")
                else:
                    raw(suffix, {"limit": limit}, expected=400)

        def inspect(stage=None):
            visibility.clear()
            subs = children()
            require(subs, "fixture_not_observed_no_subagents")
            page_check("/subagents", subs)
            visible("subagent_list_envelope_and_limit_rejection", lambda: envelope_check("/subagents", subs, False))
            root_items = sdk_list(sessions.items, sid())
            turns = all_turns()
            require(all("subagent_id" in turn for turn in turns), "turn_subagent_identity_field_missing")
            by_turn = {turn["id"]: turn for turn in turns}
            root_ids = {item["id"] for item in root_items}
            require(all(item["turn_id"] in by_turn for item in root_items), "root_items_include_child_work")
            known = {sub["id"] for sub in subs}
            root = sessions.retrieve(sid()).agent.id
            child_turn_ids = set()
            require(all(sub["session_id"] == sid() and sub["parent_agent_id"] in known | {root} for sub in subs), "subagent_parent_scope_mismatch")
            parents = {sub["id"]: sub["parent_agent_id"] for sub in subs}
            for child in parents:
                lineage = set()
                while child != root:
                    require(child not in lineage and child in parents, "invalid_subagent_parent_graph")
                    lineage.add(child)
                    child = parents[child]
            nested = [sub["id"] for sub in subs if sub["parent_agent_id"] in known]
            if args.require_nested or args.phase in ("spawn", "all"):
                require(nested, "fixture_not_observed_no_nested_child")
            require(len(subs) >= 2, "fixture_not_observed_two_children_for_scope_checks")
            seen_items, summaries = set(root_ids), []
            for sub in subs:
                child = identifier(sub["id"])
                suffix = "/subagents/" + child
                fetched = sessions.subagents.retrieve(child, session_id=sid()).to_dict()
                require(fetched == sub == raw(suffix), "subagent_retrieve_mismatch")
                raw(suffix, other=True, expected=404)
                child_turns = sdk_list(sessions.subagents.turns, child, session_id=sid())
                child_items = sdk_list(sessions.subagents.items, child, session_id=sid())
                require(child_turns and child_items, "fixture_not_observed_missing_child_history")
                require(all(turn["status"] in ("completed", "failed", "cancelled") for turn in child_turns), "child_history_not_settled")
                require(any(turn["status"] == "completed" for turn in child_turns), "fixture_not_observed_child_completion")
                require(any(item.get("type") == "message" and item.get("role") == "assistant" and item.get("content")
                            for item in child_items), "fixture_not_observed_child_model_output")
                page_check(suffix + "/turns", child_turns)
                page_check(suffix + "/items", child_items)
                visible("child_turn_list_envelope_and_limit_rejection", lambda: envelope_check(suffix + "/turns", child_turns, False))
                visible("child_item_list_envelope_and_limit_clamp", lambda: envelope_check(suffix + "/items", child_items, True))
                own_turn_ids = {turn["id"] for turn in child_turns}
                own_item_ids = {item["id"] for item in child_items}
                require(not own_item_ids & seen_items and all(item["turn_id"] in own_turn_ids for item in child_items), "items_cross_agent_boundary")
                seen_items |= own_item_ids
                collected = set()
                for turn in child_turns:
                    tid = identifier(turn["id"])
                    child_turn_ids.add(tid)
                    require(turn["subagent_id"] == child and turn["session_id"] == sid(), "child_turn_owner_mismatch")
                    # The official child Turn keeps the Session's Agent ID (SAT-08).
                    visible("child_turn_session_agent_id", lambda: require(turn["agent_id"] == root, "child_turn_agent_id_not_session_agent"))
                    fetched = sessions.subagents.turns.retrieve(tid, session_id=sid(), subagent_id=child).to_dict()
                    require(fetched == turn, "subagent_turn_retrieve_mismatch")
                    require(raw(suffix + "/turns/" + tid) == turn, "raw_child_turn_mismatch")
                    raw(suffix + "/turns/" + tid, other=True, expected=404)
                    visible("session_turn_routes_hide_child_turns", lambda: session_child_turn_check(tid))
                    items = sdk_list(sessions.subagents.turns.items, tid, session_id=sid(), subagent_id=child)
                    page_check(suffix + "/turns/" + tid + "/items", items)
                    visible("child_turn_item_list_envelope_and_limit_clamp", lambda: envelope_check(suffix + "/turns/" + tid + "/items", items, True))
                    require(all(item["turn_id"] == tid for item in items), "turn_items_wrong_turn")
                    require(items == [item for item in child_items if item["turn_id"] == tid], "per_turn_items_mismatch")
                    collected.update(item["id"] for item in items)
                require(collected == own_item_ids, "child_item_history_incomplete")
                # A real root-owned cursor/Turn must not become valid in the child scope.
                root_turn = next((turn["id"] for turn in turns if turn.get("subagent_id") is None), None)
                require(root_turn, "fixture_not_observed_root_turn")
                raw(suffix + "/turns/" + root_turn, expected=404)
                raw(suffix + "/turns/" + root_turn + "/items", expected=404)
                cursor_error(suffix + "/turns", root_turn)
                if root_items:
                    cursor_error(suffix + "/items", root_items[0]["id"])
                summaries.append({"id": child, "parent_agent_id": identifier(sub["parent_agent_id"]),
                                  "status": sub["status"], "opened_at": sub["opened_at"], "closed_at": sub["closed_at"],
                                  "turn_ids": sorted(own_turn_ids), "item_ids": sorted(own_item_ids)})
            first, second = summaries[:2]
            raw("/subagents/" + first["id"] + "/turns/" + second["turn_ids"][0], expected=404)
            cursor_error("/subagents/" + first["id"] + "/items", second["item_ids"][0])
            # Session Turn reads carry root work only (SAT-07).
            visible("session_turns_root_only", lambda: require(
                all(turn.get("subagent_id") is None and turn["agent_id"] == root for turn in turns) and
                not child_turn_ids & set(by_turn), "session_turns_include_child_work"))
            visible("session_turn_list_envelope_and_limit_rejection", lambda: envelope_check("/turns", turns, False))
            if stage in observers:
                visible("session_stream_root_work_only", lambda: stream_check(stage, set(by_turn), child_turn_ids))
            report.update(subagents=summaries, nested_ids=nested, root_item_count=len(root_items))
            report.setdefault("visibility", {})[stage or "inspect"] = dict(sorted(visibility.items()))
            for name in ("six_get_sdk_and_raw", "root_child_item_isolation", "pagination_asc_desc_after_limit",
                         "invalid_and_wrong_scope_cursors", "foreign_project_scope"):
                checked(name)
            if nested:
                checked("nested_parentage")
            failed = sorted(name for name, result in visibility.items() if result != "passed")
            require(not failed, "visibility_failed_" + "_".join(failed))
            for name in visibility:
                checked(name)
            report["resources_passed"] = True
            report["phases"]["inspect"] = "passed"
            save()

        def session_child_turn_check(tid):
            # A child Turn ID answers exactly like a missing Turn, as a path and as a cursor.
            missing = str(uuid.uuid4())
            require(raw("/turns/" + tid, expected=404) == raw("/turns/" + missing, expected=404), "child_turn_retrieve_differs_from_missing")
            require(raw("/turns", {"after": tid}, expected=404) == raw("/turns", {"after": missing}, expected=404), "child_turn_cursor_differs_from_missing")
            raw("/turns/" + tid, other=True, expected=404)
            try:
                sessions.turns.retrieve(tid, session_id=sid())
            except NotFoundError:
                return
            raise AcceptanceFailure("sdk_session_turn_retrieve_returned_child_turn")

        def stream_check(stage, root_turn_ids, child_turn_ids):
            observer = observers[stage]
            events = observer.stop()
            counts = {}
            for event in events:
                counts[event["type"]] = counts.get(event["type"], 0) + 1
            report.setdefault("streams", {})[stage] = {"kind": observer.kind, "error": observer.error, "event_counts": dict(sorted(counts.items()))}
            require(observer.error is None, "session_stream_failed")
            referenced = set().union(*(event["turn_ids"] for event in events)) if events else set()
            require(not referenced & child_turn_ids and not any(event["child_turn"] for event in events), "session_stream_carries_child_work")
            require(referenced <= root_turn_ids, "session_stream_references_unknown_turn")
            if stage in ("spawn", "spawn-direct"):
                require(counts.get("agent.session.subagent.created", 0) >= 1, "fixture_not_observed_subagent_created_event")

        marker = "subagent-proof-" + report["nonce"][:12]

        def spawn():
            submit("spawn", "Use native subagent tools for this acceptance task; do not simulate delegation. "
                   f"Create two direct children. Give the first child the exact task marker {marker}-alpha "
                   f"in its task instructions. Ask it to create a nested child with marker {marker}-nested, "
                   "ask that nested child to compute 13 + 29, wait for its answer, then report the result. "
                   f"Give the second direct child task marker {marker}-beta and ask it to compute 7 * 8. "
                   "Wait for both direct children and report their results. Keep every child open and available; "
                   "do not close or interrupt them. Use no network or file tools.")
            wait_for(lambda: len(children()) >= 3, "fixture_not_observed_spawn_and_nested", timeout=5)
            root = sessions.retrieve(sid()).agent.id
            direct = [sub for sub in children() if sub["parent_agent_id"] == root]
            require(len(direct) >= 2, "fixture_not_observed_two_direct_children")
            require(all(sub["status"] == "active" and sub["closed_at"] is None for sub in direct), "spawned_child_not_active")
            inspect("spawn")
            report["phases"]["spawn"] = "passed"
            save()

        def spawn_direct():
            submit("spawn-direct", "Use your native subagent task tools; do not simulate delegation. "
                   f"Create two direct child sessions. Give the first marker {marker}-alpha "
                   "and ask it to compute 13 + 29. Give the second marker "
                   f"{marker}-beta and ask it to compute 7 * 8. "
                   "Wait for both native child tasks and report their results. "
                   "Do not create nested children, close or interrupt either child. "
                   "Use no network or file tools.")
            wait_for(lambda: len(children()) >= 2, "fixture_not_observed_two_children", timeout=5)
            inspect("spawn-direct")
            report["phases"]["spawn-direct"] = "passed"
            save()

        def close_child():
            require(report["phases"].get("spawn") == "passed", "spawn_evidence_required")
            root = sessions.retrieve(sid()).agent.id
            before = {sub["id"]: sub for sub in children() if sub["parent_agent_id"] == root}
            require(before and all(sub["status"] == "active" for sub in before.values()), "close_requires_active_children")
            histories = {child: {
                "turns": [turn["id"] for turn in sdk_list(sessions.subagents.turns, child, session_id=sid())],
                "items": [item["id"] for item in sdk_list(sessions.subagents.items, child, session_id=sid())],
            } for child in before}
            submit("close", f"Use the native close tool to close only your existing direct child whose original task marker is {marker}-alpha. "
                   "Native processes may have been released between these root Turns. First use native resume on that SAME child ID "
                   "to load it if necessary, then invoke native close. Do not send a new task, create a replacement or merely interrupt it. "
                   "Leave the beta child open. Check the actual native close result and report failure if it failed.")
            changed = wait_for(lambda: [sub for sub in children() if sub["id"] in before and sub["status"] == "closed"],
                               "fixture_not_observed_closed_child", timeout=5)
            require(len(changed) == 1, "fixture_not_observed_single_closed_child")
            closed = changed[0]
            child = identifier(closed["id"])
            require(closed["opened_at"] == before[child]["opened_at"] and isinstance(closed["closed_at"], int), "closed_lifecycle_mismatch")
            report.update(target_id=child, target_opened_at=closed["opened_at"], target_closed_at=closed["closed_at"],
                          before_close_turns=histories[child]["turns"], before_close_items=histories[child]["items"])
            inspect("close")
            report["phases"]["close"] = "passed"
            checked("close_preserves_identity_and_opened_at")
            save()

        def resume_child():
            require(report["phases"].get("close") == "passed", "close_evidence_required")
            require(sessions.subagents.retrieve(report["target_id"], session_id=sid()).status == "closed", "resume_requires_closed_child")
            submit("resume", f"Use the native resume tool on the SAME closed child with original task marker {marker}-alpha. "
                   "Do not create a replacement. Send it a new task to compute 19 + 23, wait for its reply, "
                   "and leave it open and available. Report only after the native task finishes.")
            resumed = wait_for(lambda: next((sub for sub in children() if sub["id"] == report["target_id"] and sub["status"] == "active"), None),
                               "fixture_not_observed_resumed_child", timeout=5)
            require(resumed["opened_at"] == report["target_opened_at"] and resumed["closed_at"] is None, "resume_lifecycle_mismatch")
            own_turns = sdk_list(sessions.subagents.turns, resumed["id"], session_id=sid())
            own_items = sdk_list(sessions.subagents.items, resumed["id"], session_id=sid())
            require(set(report["before_close_turns"]) < {turn["id"] for turn in own_turns}, "fixture_not_observed_new_resumed_turn")
            require(any(turn["id"] not in report["before_close_turns"] and turn["status"] == "completed" for turn in own_turns),
                    "fixture_not_observed_resumed_turn_completion")
            require(set(report["before_close_items"]) <= {item["id"] for item in own_items}, "resume_lost_item_history")
            inspect("resume")
            report["phases"]["resume"] = "passed"
            checked("resume_same_id_opened_at_null_closed_at_and_retained_history")
            save()

        phases = {"spawn": spawn, "spawn-direct": spawn_direct, "inspect": inspect, "close": close_child, "resume": resume_child}
        for phase in ("spawn", "close", "resume") if args.phase == "all" else (args.phase,):
            phases[phase]()
        report["passed"] = all(report["phases"].get(phase) == "passed" for phase in ("spawn", "inspect", "close", "resume"))
        report["requested_phase_passed"] = True
        report.pop("failure", None)
        report.pop("http_status", None)
    except Exception as error:
        report.update(passed=False, requested_phase_passed=False,
                      failure=str(error) if isinstance(error, AcceptanceFailure) else type(error).__name__)
        if isinstance(getattr(error, "status_code", None), int):
            report["http_status"] = error.status_code
        raise
    finally:
        for connection in (client, foreign, http):
            if connection is not None:
                connection.close()
        save()
        print(json.dumps({key: report.get(key) for key in ("passed", "resources_passed", "requested_phase_passed", "session_id", "target_id", "phases", "failure")}))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # API exception messages may contain response bodies or expanded credentials.
        print(json.dumps({"error": str(error) if isinstance(error, AcceptanceFailure) else type(error).__name__}), file=sys.stderr)
        sys.exit(1)
