"""Exercise path canonicalization and response headers with the pinned SDK and raw HTTP."""

import re

import httpx2
from openai import AuthenticationError, OpenAI

REQUEST_ID = re.compile(r"req_[0-9a-f]{32}")


def verify_http_routing(base, token):
    paths = []
    record = {"request": [lambda request: paths.append(request.url.raw_path.decode())]}
    # HP-17: Core serves the canonical path instead of redirecting, so an update
    # through a non-canonical base URL applies instead of becoming a GET.
    with OpenAI(api_key=token, base_url=base + "/v1//", max_retries=0,
                http_client=httpx2.Client(trust_env=False, timeout=10, event_hooks=record)) as sdk:
        agent = sdk.beta.agents.create(model="routing-model", name="Routing")
        updated = sdk.beta.agents.update(agent.id, metadata={"route": "double-slash"})
        assert updated.id == agent.id and updated.metadata == {"route": "double-slash"}
        assert sdk.beta.agents.retrieve(agent.id).metadata == {"route": "double-slash"}
        assert paths and all(path.startswith("/v1//agents") for path in paths), paths
        # HP-23: the SDK exposes X-Request-Id on models and errors.
        assert REQUEST_ID.fullmatch(agent._request_id) and REQUEST_ID.fullmatch(updated._request_id)
        assert agent._request_id != updated._request_id
    with OpenAI(api_key="invalid-routing-key", base_url=base + "/v1", max_retries=0,
                http_client=httpx2.Client(trust_env=False, timeout=10)) as invalid:
        try:
            invalid.beta.agents.retrieve(agent.id)
        except AuthenticationError as error:
            assert REQUEST_ID.fullmatch(error.request_id)
            assert error.body["type"] == "invalid_request_error" and error.body["code"] is None
        else:
            raise AssertionError("An invalid key was accepted")

    headers = {"Authorization": "Bearer " + token, "OpenAI-Beta": "agents=v1"}
    resource = base + "/v1/agents/" + agent.id
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        expected = raw.get(resource, headers=headers).json()
        for url in (base + "/v1/agents/x/../" + agent.id, base + "/v1/agents/" + agent.id.replace("-", "%2D")):
            response = raw.get(url, headers=headers)
            assert response.status_code == 200 and response.json() == expected, url
        # HP-19: HEAD answers GET routes without a body.
        head = raw.head(resource, headers=headers)
        assert head.status_code == 200 and head.content == b"" and int(head.headers["content-length"]) > 0
        # HP-20: 405 keeps Core's body and lists the route's methods.
        response = raw.put(resource, headers=headers, json={})
        assert response.status_code == 405 and response.headers["allow"] == "GET,HEAD,POST,DELETE"
        assert response.json()["error"]["code"] == "unsupported_operation"
        # HP-05/HP-02/HP-07: Beta first, then the invalid_request_error 401.
        response = raw.get(resource)
        assert response.status_code == 400 and response.json()["error"] == {
            "type": "invalid_beta", "code": "invalid_beta", "param": None,
            "message": "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'."}
        response = raw.get(resource, headers=[("OpenAI-Beta", "agents=v1"), ("OpenAI-Beta", "assistants=v2"),
                                              ("Authorization", "Bearer " + token)])
        assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_beta"
        unauthorized = raw.get(resource, headers={"OpenAI-Beta": "agents=v1"})
        assert unauthorized.status_code == 401 and unauthorized.json()["error"]["code"] is None
        files = raw.get(base + "/v1/files", headers={"Authorization": "Bearer invalid-routing-key"})
        assert files.status_code == 401 and files.json()["error"]["code"] == "invalid_api_key"
        # HP-23/HP-24: every response carries a fresh ID and the OpenAI headers.
        ids = set()
        for response in (head, unauthorized, files, raw.put(resource, headers=headers, json={})):
            assert REQUEST_ID.fullmatch(response.headers["x-request-id"]) and response.headers["x-request-id"] not in ids
            ids.add(response.headers["x-request-id"])
            assert response.headers["openai-version"] == "2020-10-01"
            assert int(response.headers["openai-processing-ms"]) >= 0
            assert response.headers["x-content-type-options"] == "nosniff"
        assert raw.delete(resource, headers=headers).status_code == 200
    print("HTTP routing: pinned SDK update through a non-canonical path, request IDs, HEAD, Allow, "
          "Beta-before-authentication and 401 envelopes passed.")
