"""List query acceptance through real HTTP/PostgreSQL and the fixed official SDK.

Owned fixtures use real Worker admission with dispatch paused. No native executor
or model runs, and initial Turn/Item history remains visible throughout the test.
"""

import importlib.metadata
import json
import secrets
import sys
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import APIStatusError, DefaultHttpxClient, OpenAI


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
        print(json.dumps({"result": "passed", "families": len(families), "sdk_and_raw_rejections": rejected,
                          "sdk_empty_order_omitted": len(families), "retained_turns": len(turns), "retained_items": len(items), "postgres": True,
                          "worker_admission": True, "native_model_execution": False}))


if __name__ == "__main__":
    main()
