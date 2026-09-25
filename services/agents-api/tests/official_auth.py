"""Exercise caller scope through the pinned SDK, raw HTTP and service restart."""

import uuid

import httpx2 as httpx
from openai import AuthenticationError


def verify_caller_principals(client, base, binding, token, rotated, peer, session):
    scope = {"organization": binding["organization_id"], "project": binding["project_id"]}
    for key in (token, rotated, peer):
        with client(key, **scope) as scoped:
            assert scoped.beta.agents.sessions.retrieve(session.id) == session
            assert session.id in {item.id for item in scoped.beta.agents.sessions.list()}
    for mismatch in ({**scope, "organization": "wrong-org"}, {**scope, "project": "wrong-project"}):
        with client(token, **mismatch) as invalid:
            try:
                invalid.beta.agents.sessions.retrieve(session.id)
            except AuthenticationError as error:
                # Beta 401s are invalid_request_error with a null code (HP-07).
                assert error.body["type"] == "invalid_request_error" and error.body["code"] is None
            else:
                raise AssertionError("Untrusted scope header was accepted")
    headers = [("Authorization", "Bearer " + token), ("OpenAI-Beta", "agents=v1")]
    path = base + "/v1/agents/sessions/" + session.id
    with httpx.Client(trust_env=False, timeout=10) as raw:
        for extra in ([('OpenAI-Project', binding['project_id'])] * 2,
                      [('OpenAI-Organization', binding['organization_id']), ('OpenAI-Organization', 'other')],
                      [('Authorization', 'Bearer ' + peer)]):
            response = raw.get(path, headers=headers + extra)
            error = response.json()["error"]
            assert response.status_code == 401 and error["type"] == "invalid_request_error" and error["code"] is None
        response = raw.get(path, headers=headers + [("X-Tenant-ID", str(uuid.uuid4())), ("X-User-ID", "forged")])
        assert response.status_code == 200 and response.json()["id"] == session.id


def verify_scope_recovery(client, base, binding, keys, session):
    # The caller has restarted Core without any business key configuration.
    for key in keys:
        verify_caller_principals(client, base, binding, key, keys[1], keys[2], session)
    print("Caller scope: database Projects and all issued keys survived restart with unchanged scope.")
