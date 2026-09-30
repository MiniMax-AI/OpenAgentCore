"""List query acceptance through real HTTP/PostgreSQL and the fixed official SDK.

Owned fixtures use real Worker admission with dispatch paused. No native executor
or model runs, and initial Turn/Item history remains visible throughout the test.
The tolerance and cursor checks follow contracts/agents-api/wire-semantics.md#lists.
"""

import importlib.metadata
import json
import secrets
import sys
import uuid
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import APIStatusError, DefaultHttpxClient, OpenAI


BETA_DUPLICATE = "Failed to deserialize query string: duplicate field `{}`"
BETA_DIGIT = "Failed to deserialize query string: limit: invalid digit found in string"
SKILLS_DUPLICATE = ("Duplicate parameter: '{0}'. You provided multiple values for this parameter, whereas only one is "
                    "allowed. If you are trying to provide a list of values, use the array syntax instead e.g. '{0}[]=<value>'.")
# Limit policy per family: accepted limit -> page size, or the rejected error fields.
LIMITS = {
    "clamp": {"0": 1, "101": None, "-1": ("invalid_request_error", None, BETA_DIGIT), "abc": ("invalid_request_error", None, BETA_DIGIT)},
    "strict": {"0": ("invalid_request_error", None, "limit must be between 1 and 100"), "101": ("invalid_request_error", None, "limit must be between 1 and 100"),
               "-1": ("invalid_request_error", None, BETA_DIGIT), "abc": ("invalid_request_error", None, BETA_DIGIT)},
    "vault": {"0": 1, "-1": 1, "101": None, "abc": ("invalid_request_error", None, BETA_DIGIT)},
    "skills": {"0": 0, "101": ("integer_above_max_value", "limit", "Invalid 'limit': integer above maximum value. Expected a value <= 100, but got 101 instead."),
               "-1": ("integer_below_min_value", "limit", "Invalid 'limit': integer below minimum value. Expected a value >= 0, but got -1 instead.")},
    "files": {"0": (None, None, "limit must be between 1 and 10000."), "10001": (None, None, "limit must be between 1 and 10000.")},
}


# Unresolved `after` cursors (cursor error batch rows C1, C2, C5 and K1).
LOOKUP_MISSING = {"message": "Resource not found.", "type": "not_found_error", "code": "not_found_error", "param": None}
SKILLS_MISSING = {"message": "Resource not found.", "type": "invalid_request_error", "code": None, "param": None}
FILES_MISSING = {"message": "Resource not found.", "type": "invalid_request_error", "code": None, "param": "after"}
ITEM_CURSOR = {"message": "Invalid session item ID in `after`", "type": "invalid_request_error",
               "code": "invalid_request_error", "param": None}
OTHER_SKILL_VERSION = {"message": "Skill version cursor does not match this skill.", "type": "invalid_request_error",
                       "code": "invalid_value", "param": "after"}


def version_prefix_error(value):
    return {"message": f"Invalid 'after': '{value}'. Expected an ID that begins with 'skillver'.",
            "type": "invalid_request_error", "code": "invalid_value", "param": "after"}


def verify_cursors(raw, base, token, foreign, client, owned, session_id, vault_id, cleanup):
    """Checks unresolved cursors through raw HTTP and the SDK for tenants A and B.

    Tenant B owns one resource per family, so its IDs are real foreign cursors,
    and tenant A's IDs are foreign cursors for B. Subagent and Artifact lists need
    seeded execution history and are covered by the Go PostgreSQL tests.
    """
    checks = 0
    other = cleanup.enter_context(OpenAI(api_key=foreign, base_url=base + "/v1", max_retries=0,
                                         http_client=DefaultHttpxClient(trust_env=False)))
    b_agent = other.beta.agents.create(model="query-fixture-model", name="Foreign Cursor Agent")
    cleanup.callback(other.beta.agents.delete, b_agent.id)
    b_template = other.beta.agents.environments.templates.create(name="Foreign Cursor Template")
    cleanup.callback(other.beta.agents.environments.templates.delete, b_template.id)
    b_vault = other.beta.agents.vaults.create(name="Foreign Cursor Vault")
    cleanup.callback(other.beta.agents.vaults.delete, b_vault.id)
    b_credential = other.beta.agents.vaults.credentials.create(
        b_vault.id, name="Foreign Cursor Credential",
        auth={"type": "static_bearer", "mcp_server_url": "https://query.example.invalid/mcp", "token": "foreign-" + secrets.token_hex(8)})
    b_session = other.beta.agents.sessions.create(agent_id=b_agent.id, environment={"type": "none"}, input="Foreign cursor history.")
    cleanup.callback(other.beta.agents.sessions.delete, b_session.id)
    other.beta.agents.sessions.events.create(b_session.id, events=[{"type": "agent.session.input.cancel"}])
    b_turn = other.beta.agents.sessions.turns.list(b_session.id).data[0].id
    b_item = other.beta.agents.sessions.items.list(b_session.id).data[0].id
    b_file = other.files.create(file=("foreign-cursor.txt", b"Foreign cursor fixture"), purpose="user_data")
    cleanup.callback(other.files.delete, b_file.id)
    manifest = b"---\nname: foreign-cursor\ndescription: Foreign cursor fixture.\n---\nCursor.\n"
    b_skill = other.skills.create(files=[("foreign-cursor/SKILL.md", manifest, "text/markdown")])
    cleanup.callback(other.skills.delete, b_skill.id)
    b_version = other.skills.versions.list(b_skill.id).data[0].id

    agents, sessions, vaults = client.beta.agents, client.beta.agents.sessions, client.beta.agents.vaults
    skill = owned["skills"][0]
    first_version = client.skills.versions.list(skill, order="asc").data[0].id
    manifest = b"---\nname: query-0\ndescription: Second owned version.\n---\nCursor.\n"
    later_version = client.skills.versions.create(skill, files=[("query-0/SKILL.md", manifest, "text/markdown")]).id
    other_version = client.skills.versions.list(owned["skills"][1]).data[0].id
    other_turn = sessions.turns.list(owned["sessions"][1]).data[0].id
    other_item = sessions.items.list(owned["sessions"][1]).data[0].id

    def expect(key, path, status, error, cursors, listing=None, beta=True):
        nonlocal checks
        headers = {"Authorization": "Bearer " + key}
        if beta:
            headers["OpenAI-Beta"] = "agents=v1"
        for cursor in cursors:
            response = raw.get(base + "/v1" + path, headers=headers, params={"after": cursor})
            assert response.status_code == status and response.json() == {"error": error}, (path, cursor, response.status_code, response.text)
            if listing is not None:
                try:
                    listing(after=cursor)
                except APIStatusError as sdk_error:
                    assert sdk_error.status_code == status and sdk_error.body == error, (path, cursor, sdk_error.body)
                else:
                    raise AssertionError(f"SDK accepted {path} cursor {cursor}")
            checks += 1

    random = str(uuid.uuid4())
    malformed = ["not-a-valid-id", str(uuid.UUID(int=0))]
    # C1: lookup-family lists answer malformed, other-type, other-parent and foreign
    # cursors exactly like a missing one.
    lookups = [
        ("/agents", agents.list, ["agent_" + secrets.token_hex(25), owned["sessions"][0], b_agent.id]),
        ("/agents/environments/templates", agents.environments.templates.list, ["envtmpl_" + secrets.token_hex(25), owned["vaults"][0], b_template.id]),
        ("/agents/sessions", sessions.list, ["sess_" + secrets.token_hex(25), owned["agents"][0], b_session.id]),
        (f"/agents/sessions/{session_id}/turns", lambda **q: sessions.turns.list(session_id, **q), ["turn_" + secrets.token_hex(25), owned["items"][0], other_turn, b_turn]),
        ("/vaults", vaults.list, ["vault_" + secrets.token_hex(25), owned["credentials"][0], b_vault.id]),
        (f"/vaults/{vault_id}/credentials", lambda **q: vaults.credentials.list(vault_id, **q), ["credential_" + secrets.token_hex(25), vault_id, b_credential.id]),
    ]
    for path, listing, cursors in lookups:
        expect(token, path, 404, LOOKUP_MISSING, [random, *malformed, *cursors], listing)
    for path, _, _ in lookups[:3] + lookups[4:5]:
        expect(foreign, path, 404, LOOKUP_MISSING, [owned["agents"][0], owned["sessions"][0], owned["templates"][0], owned["vaults"][0]])
    expect(foreign, f"/agents/sessions/{b_session.id}/turns", 404, LOOKUP_MISSING, owned["turns"])
    expect(foreign, f"/vaults/{b_vault.id}/credentials", 404, LOOKUP_MISSING, owned["credentials"])
    # C2: any cursor that is not an Item of this Session.
    items = lambda **q: sessions.items.list(session_id, **q)
    expect(token, f"/agents/sessions/{session_id}/items", 400, ITEM_CURSOR,
           [random, *malformed, "msg_" + secrets.token_hex(25), owned["turns"][0], other_item, b_item], items)
    expect(foreign, f"/agents/sessions/{b_session.id}/items", 400, ITEM_CURSOR, owned["items"])
    # C5: Skill versions tell non-version values, other Skills' versions and missing versions apart.
    versions = lambda **q: client.skills.versions.list(skill, **q)
    for value in ("not-a-valid-id", skill, random):
        expect(token, f"/skills/{skill}/versions", 400, version_prefix_error(value), [value], versions, beta=False)
    # A long or unprintable value is not repeated in the message.
    unechoed = {**version_prefix_error(""), "message": "Invalid 'after'. Expected an ID that begins with 'skillver'."}
    expect(token, f"/skills/{skill}/versions", 400, unechoed, ["x" * 300, "bad\x01value"], versions, beta=False)
    expect(token, f"/skills/{skill}/versions", 400, OTHER_SKILL_VERSION, [other_version], versions, beta=False)
    expect(token, f"/skills/{skill}/versions", 404, SKILLS_MISSING, ["skillver_" + random, "skillver_not-a-uuid", b_version], versions, beta=False)
    expect(foreign, f"/skills/{b_skill.id}/versions", 404, SKILLS_MISSING, [first_version, later_version], beta=False)
    assert [value.id for value in versions(order="asc", after=first_version)] == [later_version]
    # K1: Files and Skills keep their missing-cursor errors.
    expect(token, "/files", 404, FILES_MISSING, ["file-" + secrets.token_hex(12), "not-a-valid-id", b_file.id], client.files.list, beta=False)
    expect(token, "/skills", 404, SKILLS_MISSING, ["skill_" + random, "not-a-valid-id", b_skill.id], client.skills.list, beta=False)
    # K3: a foreign or missing parent is 404 before its cursor is read.
    for foreign_path, missing_path, beta in (
            (f"/agents/sessions/{session_id}/turns", f"/agents/sessions/{random}/turns", True),
            (f"/agents/sessions/{session_id}/items", f"/agents/sessions/{random}/items", True),
            (f"/vaults/{vault_id}/credentials", f"/vaults/{random}/credentials", True),
            (f"/skills/{skill}/versions", f"/skills/skill_{random}/versions", False)):
        headers = {"Authorization": "Bearer " + foreign, **({"OpenAI-Beta": "agents=v1"} if beta else {})}
        missing = raw.get(base + "/v1" + missing_path, headers=headers)
        assert missing.status_code == 404, missing_path
        expect(foreign, foreign_path, 404, missing.json()["error"], ["not-a-valid-id", owned["items"][0], b_item, first_version], beta=beta)
    return checks


def verify_tolerance(raw, url, headers, foreign_headers, name, listing, owned, policy, nested, private):
    """Checks unknown/repeated keys and limit policy for one family as tenant A and B."""
    checks = 0

    def get(params, owner=True):
        response = raw.get(url, headers=headers if owner else foreign_headers, params=params)
        # Errors and tenant B responses never disclose owned resources or secrets.
        if not owner or response.status_code != 200:
            assert all(value not in response.text for value in private), (name, params)
        return response

    def foreign_matches(params, plain):
        # Tenant B sees its own view: an empty top-level page or the same parent 404.
        response = get(params, owner=False)
        expected = get(plain, owner=False)
        assert response.status_code == expected.status_code == (404 if nested else 200), (name, params)
        assert response.json() == expected.json(), (name, params)
        if not nested:
            assert response.json()["data"] == [] and response.json()["has_more"] is False, (name, params)

    def rejected(params, code, param, message):
        nonlocal checks
        response = get(params)
        assert response.status_code == 400, (name, params, response.status_code)
        error = response.json()["error"]
        assert (error["type"], error["code"], error["param"], error["message"]) == ("invalid_request_error", code, param, message), (name, params, error)
        # Query parsing precedes resource lookup, so tenant B receives the same error.
        foreign = get(params, owner=False)
        assert foreign.status_code == 400 and foreign.json() == response.json(), (name, params)
        checks += 1

    # A1: unknown keys are ignored for both tenants and through the SDK.
    plain = {"order": "asc", "limit": 1}
    baseline = get(plain).json()
    for extra in ({"query_probe": "1"}, {"tenant_id": "foreign", "limit[]": "5"}, [("query_probe", "1"), ("query_probe", "2")]):
        params = list(plain.items()) + (list(extra.items()) if isinstance(extra, dict) else extra)
        assert get(params).json() == baseline, (name, extra)
        foreign_matches(params, plain)
        checks += 1
    assert [value.id for value in listing(order="asc", limit=1, extra_query={"query_probe": "1"}).data] == owned[:1], name

    # B1-B3: a repeated supported key rejects with the family's observed fields.
    repeated = [("limit", "1"), ("limit", "2")]
    if policy == "skills":
        rejected(repeated, "duplicate_parameter", "limit", SKILLS_DUPLICATE.format("limit"))
    elif policy == "files":
        rejected(repeated, "unsupported_parameter", None, "Supported list parameters are after, limit, order and purpose, each supplied once.")
    else:
        rejected(repeated, "invalid_request_error", None, BETA_DUPLICATE.format("limit"))
        rejected([("order", "asc"), ("order", "asc")], "invalid_request_error", None, BETA_DUPLICATE.format("order"))

    # C1-C7: limit bounds.
    for limit, outcome in LIMITS[policy].items():
        params = {"order": "asc", "limit": limit}
        if isinstance(outcome, tuple):
            rejected(params, *outcome)
            continue
        size = len(owned) if outcome is None else outcome
        body = get(params).json()
        assert [value["id"] for value in body["data"]] == owned[:size], (name, limit)
        assert body["has_more"] is (size < len(owned)), (name, limit)
        assert (body["first_id"], body["last_id"]) == ((owned[0], owned[size - 1]) if size else (None, None)), (name, limit)
        foreign_matches(params, {"order": "asc"})
        checks += 1
    if policy == "skills":
        # A zero page reports only whether a resource follows the cursor.
        tail = get({"order": "asc", "limit": 0, "after": owned[-1]}).json()
        assert tail == {"object": "list", "data": [], "has_more": False, "first_id": None, "last_id": None}, name
        assert listing(limit=0).data == [] and listing(limit=0).has_more is True, name
        checks += 1
    return checks

def verify_resource_queries(raw, base, token, foreign, client, owned, session_id, vault_id):
    """A2: single-resource routes ignore unknown query keys for both tenants."""
    probe = "?tenant_id=foreign&include=files&cascade=true&query_probe=1&query_probe=2"
    private = [value for values in owned.values() for value in values]
    beta = {"OpenAI-Beta": "agents=v1"}
    checks = 0

    def call(method, path, key, query="", headers=beta, **body):
        return raw.request(method, base + "/v1" + path + query, headers={**headers, "Authorization": "Bearer " + key}, **body)

    def same(method, path, status, target, headers=beta, **body):
        nonlocal checks
        for key, expected in ((token, status), (foreign, 404)):
            plain, probed = call(method, path, key, "", headers, **body), call(method, path, key, probe, headers, **body)
            assert plain.status_code == probed.status_code == expected, (method, path, plain.status_code, probed.status_code)
            assert plain.content == probed.content, (method, path)
        # A foreign resource stays indistinguishable from a missing one of the same shape.
        assert all(value not in probed.text for value in private), path
        absent = target[:-1] + ("1" if target.endswith("0") else "0")
        missing = call(method, path.replace(target, absent), token, probe, headers, **body)
        assert missing.status_code == 404 and missing.json() == probed.json(), (path, missing.text, probed.text)
        checks += 1

    for prefix, target in (("/agents/", owned["agents"][0]), ("/agents/environments/templates/", owned["templates"][0]),
                           ("/agents/sessions/", session_id), (f"/agents/sessions/{session_id}/turns/", owned["turns"][0]),
                           ("/vaults/", vault_id), (f"/vaults/{vault_id}/credentials/", owned["credentials"][0])):
        same("GET", prefix + target, 200, target)
    for prefix, target in (("/files/", owned["files"][0]), ("/skills/", owned["skills"][0])):
        same("GET", prefix + target, 200, target, headers={})
    # An empty event batch is an authorized no-op; the unknown key changes nothing.
    same("POST", f"/agents/sessions/{session_id}/events", 202, session_id, json={"events": []})
    with raw.stream("GET", base + f"/v1/agents/sessions/{session_id}/events" + probe,
                    headers={**beta, "Authorization": "Bearer " + token}) as stream:
        assert stream.status_code == 200 and stream.headers["content-type"].startswith("text/event-stream")
    checks += 1

    # Writes with unknown keys apply normally for the owner and stay 404 for tenant B.
    templates, vaults = client.beta.agents.environments.templates, client.beta.agents.vaults
    template, vault = templates.create(name="Query Probe Template"), vaults.create(name="Query Probe Vault")
    for method, path, body in (("POST", "/agents/environments/templates/" + template.id, {"json": {"name": "Foreign"}}),
                               ("DELETE", "/agents/environments/templates/" + template.id, {}),
                               ("DELETE", "/vaults/" + vault.id, {})):
        assert call(method, path, foreign, probe, **body).status_code == 404, (method, path)
    assert templates.retrieve(template.id) == template and vaults.retrieve(vault.id) == vault
    renamed = call("POST", "/agents/environments/templates/" + template.id, token, probe, json={"name": "Renamed Probe"})
    assert renamed.status_code == 200 and renamed.json()["name"] == "Renamed Probe"
    for path, kind in (("/agents/environments/templates/" + template.id, "agent.environment.template.deleted"), ("/vaults/" + vault.id, "vault.deleted")):
        deleted = call("DELETE", path, token, probe)
        assert deleted.status_code == 200 and deleted.json() == {"id": path.rsplit("/", 1)[1], "object": kind, "deleted": True}, path
        assert call("GET", path, token).status_code == 404, path

    # Uploads: query keys are not form fields and never select a tenant.
    upload = client.files.create(file=("query-probe.txt", b"probe"), purpose="user_data",
                                 extra_query={"purpose": "assistants", "tenant_id": "foreign"})
    assert upload.purpose == "user_data" and call("GET", "/files/" + upload.id, foreign, probe, headers={}).status_code == 404
    rejected = raw.post(base + "/v1/files?purpose=user_data", headers={"Authorization": "Bearer " + token},
                        files={"file": ("query-probe.txt", b"probe")}, data={"purpose": "assistants"})
    assert rejected.status_code == 400
    client.files.delete(upload.id)
    manifest = b"---\nname: query-probe\ndescription: Query probe.\n---\nProbe.\n"
    bundle = [("query-probe/SKILL.md", manifest, "text/markdown")]
    skill = client.skills.create(files=bundle, extra_query={"tenant_id": "foreign", "default": "true"})
    target = "/skills/" + skill.id + "/versions"
    multipart = {"files": [("files[]", ("query-probe/SKILL.md", manifest, "text/markdown"))]}
    denied = call("POST", target, foreign, probe, headers={}, **multipart)
    absent = skill.id[:-1] + ("1" if skill.id.endswith("0") else "0")
    missing = call("POST", "/skills/" + absent + "/versions", token, probe, headers={}, **multipart)
    assert denied.status_code == missing.status_code == 404 and denied.json() == missing.json()
    version = client.skills.versions.create(skill.id, files=bundle, extra_query={"default": "true", "tenant_id": "foreign"})
    assert version.version == "2" and client.skills.retrieve(skill.id).default_version == "1"
    client.skills.delete(skill.id)
    return checks + 5

def main():
    base, token, foreign = sys.argv[1:]
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    with ExitStack() as cleanup:
        raw = cleanup.enter_context(httpx2.Client(trust_env=False, timeout=10))
        client = cleanup.enter_context(OpenAI(
            api_key=token, base_url=base + "/v1", max_retries=0,
            _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False),
        ))
        agents = client.beta.agents
        sessions = agents.sessions
        templates = agents.environments.templates
        vaults = agents.vaults
        secret = "query-credential-" + secrets.token_hex(16)
        owned = {name: [] for name in ("agents", "templates", "vaults", "credentials", "sessions", "files", "skills")}
        for index in range(2):
            agent = agents.create(model="query-fixture-model", name=f"Query Agent {index}")
            owned["agents"].append(agent.id)
            cleanup.callback(agents.delete, agent.id)
            template = templates.create(name=f"Query Template {index}")
            owned["templates"].append(template.id)
            cleanup.callback(templates.delete, template.id)
            vault = vaults.create(name=f"Query Vault {index}")
            owned["vaults"].append(vault.id)
            cleanup.callback(vaults.delete, vault.id)
            file = client.files.create(file=(f"query-{index}.txt", b"Owned list query fixture"), purpose="user_data")
            owned["files"].append(file.id)
            cleanup.callback(client.files.delete, file.id)
            manifest = f"---\nname: query-{index}\ndescription: Owned query fixture.\n---\nList metadata only.\n"
            skill = client.skills.create(files=[(f"query-{index}/SKILL.md", manifest.encode(), "text/markdown")])
            owned["skills"].append(skill.id)
            cleanup.callback(client.skills.delete, skill.id)
            session = sessions.create(
                agent_id=agent.id, environment={"type": "none"},
                input=f"Retain query fixture initial history {index}.",
            )
            owned["sessions"].append(session.id)
            cleanup.callback(sessions.delete, session.id)
            sessions.events.create(session.id, events=[{"type": "agent.session.input.cancel"}])
        vault_id = owned["vaults"][0]
        for index in range(2):
            credential = vaults.credentials.create(
                vault_id, name=f"Query Credential {index}",
                auth={"type": "static_bearer", "mcp_server_url": "https://query.example.invalid/mcp", "token": secret},
            )
            owned["credentials"].append(credential.id)
        session_id = owned["sessions"][0]
        for index in range(2):
            sessions.events.create(session_id, events=[{
                "type": "agent.session.input.message",
                "input": [{"role": "user", "content": [{"type": "input_text", "text": f"Retain query history {index}."}]}],
            }])
            sessions.events.create(session_id, events=[{"type": "agent.session.input.cancel"}])
        turns = list(sessions.turns.list(session_id, order="asc"))
        items = list(sessions.items.list(session_id, order="asc"))
        assert len(turns) == len(items) == 3
        assert all(turn.status == "cancelled" for turn in turns)
        owned["turns"] = [turn.id for turn in turns]
        owned["items"] = [item.id for item in items]

        families = [
            ("agents", "/agents", agents.list, True, False),
            ("templates", "/agents/environments/templates", templates.list, True, False),
            ("sessions", "/agents/sessions", sessions.list, True, False),
            ("turns", f"/agents/sessions/{session_id}/turns", lambda **q: sessions.turns.list(session_id, **q), True, True),
            ("items", f"/agents/sessions/{session_id}/items", lambda **q: sessions.items.list(session_id, **q), True, True),
            ("vaults", "/vaults", vaults.list, True, False),
            ("credentials", f"/vaults/{vault_id}/credentials", lambda **q: vaults.credentials.list(vault_id, **q), True, True),
            ("files", "/files", client.files.list, False, False),
            ("skills", "/skills", client.skills.list, False, False),
        ]
        policies = {"agents": "clamp", "templates": "clamp", "sessions": "clamp", "items": "clamp", "turns": "strict",
                    "vaults": "vault", "credentials": "vault", "files": "files", "skills": "skills"}
        tolerated = 0
        state_before = {name: [value.to_dict() for value in listing(order="asc", limit=1)] for name, _, listing, _, _ in families}
        rejected = 0
        for name, path, listing, beta, nested in families:
            headers = {"Authorization": "Bearer " + token}
            if beta:
                headers["OpenAI-Beta"] = "agents=v1"
            url = base + "/v1" + path
            before = state_before[name]
            assert [value["id"] for value in before] == owned[name], name
            assert [value.id for value in listing(order="desc", limit=1)] == owned[name][::-1], name
            assert [value.id for value in listing(limit=1)] == owned[name][::-1], name
            page = raw.get(url, headers=headers, params={"order": "asc", "limit": 1})
            assert page.status_code == 200 and page.json()["has_more"] is True, name
            assert page.json()["first_id"] == page.json()["last_id"] == owned[name][0], name
            tail = raw.get(url, headers=headers, params={"order": "asc", "after": owned[name][-1], "limit": 1})
            assert tail.status_code == 200 and tail.json()["data"] == [] and tail.json()["has_more"] is False, name

            expected_code = "invalid_request_error" if beta else ("invalid_value" if name == "skills" else None)
            expected_param = "order" if name == "skills" else None
            for order in ("", "sideways"):
                response = raw.get(url, headers=headers, params={"order": order})
                assert response.status_code == 400, (name, order, response.status_code)
                body = response.json()["error"]
                assert body["type"] == "invalid_request_error" and body["code"] == expected_code and body["param"] == expected_param, (name, body)
                assert isinstance(body["message"], str) and body["message"], name
                assert secret not in response.text and all(value not in response.text for value in owned[name]), name
                if order:
                    try:
                        listing(order=order)
                    except APIStatusError as error:
                        assert error.status_code == 400 and error.body == body, (name, error.body)
                    else:
                        raise AssertionError(f"SDK accepted invalid {name} order")
                else:
                    # Fixed 3.13 drops empty query values in _qs.stringify_items.
                    # Only the raw request above exercises an explicit order=.
                    assert [value.id for value in listing(order="", limit=1)] == owned[name][::-1], name
                foreign_error = raw.get(url, headers={**headers, "Authorization": "Bearer " + foreign}, params={"order": order})
                assert foreign_error.status_code == 400 and foreign_error.json() == response.json(), name
                unauthorized = raw.get(url, headers={k: v for k, v in headers.items() if k != "Authorization"}, params={"order": order})
                assert unauthorized.status_code == 401 and secret not in unauthorized.text, name
                rejected += 2 if order else 1

            foreign_page = raw.get(url, headers={**headers, "Authorization": "Bearer " + foreign}, params={"order": "asc"})
            if nested:
                assert foreign_page.status_code == 404, name
            else:
                assert foreign_page.status_code == 200 and foreign_page.json()["data"] == [], name
            assert secret not in foreign_page.text and all(value not in foreign_page.text for value in owned[name]), name
            private = [secret] + [value for values in owned.values() for value in values]
            foreign_headers = {**headers, "Authorization": "Bearer " + foreign}
            tolerated += verify_tolerance(raw, url, headers, foreign_headers, name, listing, owned[name], policies[name], nested, private)
            if name in {"vaults", "credentials"}:
                # D1: a scalar status and status[] filter by their union.
                union = raw.get(url, headers=headers, params=[("order", "asc"), ("status", "active"), ("status[]", "archived")])
                assert union.status_code == 200 and union.json() == raw.get(url, headers=headers, params={"order": "asc"}).json(), name
                assert [value.id for value in listing(order="asc", status="active", extra_query={"status[]": "archived"})] == owned[name], name
                duplicate = raw.get(url, headers=headers, params=[("status", "active"), ("status", "active")])
                assert duplicate.status_code == 400 and duplicate.json()["error"]["message"] == BETA_DUPLICATE.format("status"), name
                assert raw.get(url, headers=foreign_headers, params=[("status", "active"), ("status", "active")]).json() == duplicate.json(), name
                tolerated += 2
            if name == "files":
                # D2: an explicit empty purpose is the same as omission.
                for query in ({"purpose": ""}, {"purpose": "", "query_probe": "1"}):
                    response = raw.get(url, headers=headers, params={"order": "asc", **query})
                    assert response.status_code == 200 and [value["id"] for value in response.json()["data"]] == owned[name], name
                    foreign_page = raw.get(url, headers=foreign_headers, params={"order": "asc", **query})
                    assert foreign_page.status_code == 200 and foreign_page.json()["data"] == [], name
                tolerated += 1
            if name in {"vaults", "credentials"}:
                for query in ({"status": "query-invalid"}, {"status[]": "query-invalid"}):
                    response = raw.get(url, headers=headers, params=query)
                    assert response.status_code == 400, name
                    error = response.json()["error"]
                    assert (error["type"], error["code"], error["param"]) == ("invalid_request_error", "invalid_request_error", None), (name, error)
                    try:
                        listing(extra_query=query)
                    except APIStatusError as sdk_error:
                        assert sdk_error.status_code == 400 and sdk_error.body == error, name
                    else:
                        raise AssertionError(f"SDK accepted invalid {name} status")
                    rejected += 2
            assert [value.to_dict() for value in listing(order="asc", limit=1)] == before, name
        assert {name: [value.to_dict() for value in listing(order="asc", limit=1)] for name, _, listing, _, _ in families} == state_before
        assert list(sessions.turns.list(session_id, order="asc")) == turns
        assert list(sessions.items.list(session_id, order="asc")) == items
        cursors = verify_cursors(raw, base, token, foreign, client, owned, session_id, vault_id, cleanup)
        resources = verify_resource_queries(raw, base, token, foreign, client, owned, session_id, vault_id)
        print(json.dumps({"single_resource_checks": resources, "cursor_checks": cursors, "result": "passed", "families": len(families), "sdk_and_raw_rejections": rejected, "tolerance_checks": tolerated,
                          "sdk_empty_order_omitted": len(families), "retained_turns": len(turns), "retained_items": len(items), "postgres": True,
                          "worker_admission": True, "native_model_execution": False}))


if __name__ == "__main__":
    main()
