"Verify pinned Session metadata replacement through the actual HTTP service."

import json
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, ConflictError, NotFoundError

import official_body


def without_metadata(session):
    return {key: value for key, value in session.to_dict().items() if key != "metadata"}


def verify_session_metadata(client, other, invalid, spec, expect_error):
    sessions = client.beta.agents.sessions
    original = {"old": "remove", "keep": "replace"}
    retry = {"Idempotency-Key": str(uuid.uuid4())}
    first = sessions.create(**spec, metadata=original, extra_headers=retry)
    fixed = without_metadata(first)
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    url = str(client.base_url).rstrip("/") + "/agents/sessions/" + first.id

    def assert_metadata(session, expected):
        assert session.metadata == expected
        assert without_metadata(session) == fixed
        assert sessions.retrieve(first.id) == session
        assert next(item for item in sessions.list(limit=1) if item.id == first.id) == session
        return session

    expect_error(BadRequestError, lambda: sessions.update(first.id))
    assert_metadata(sessions.retrieve(first.id), original)
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for metadata in [{"keep": "new", "empty": ""}, None, {},
                         {"🧪" * 64: "界" * 512, **{str(i): "🧪" * 512 for i in range(15)}}]:
            expected = metadata or {}
            assert_metadata(sessions.update(first.id, metadata=metadata), expected)
            # Reset between HTTP and SDK updates so both must actually replace values.
            sessions.update(first.id, metadata=original)
            response = raw.post(url, headers=headers, json={"metadata": metadata})
            assert response.status_code == 200 and response.headers["cache-control"] == "no-store"
            assert response.json()["metadata"] == expected
            current = assert_metadata(sessions.retrieve(first.id), expected)
            assert response.json() == current.to_dict()
            expect_error(BadRequestError, lambda: sessions.update(first.id))
            empty = raw.post(url, headers=headers, json={})
            assert empty.status_code == 400 and empty.json()["error"]["code"] == "invalid_request_error"
            # Updating metadata must not rewrite or reapply the original creation request.
            assert sessions.create(**spec, metadata=original, extra_headers=retry) == current
            expect_error(ConflictError, lambda: sessions.create(**spec, metadata=metadata, extra_headers=retry))

        invalid_fields = [
            {"metadata": []}, {"metadata": "value"}, {"metadata": False},
            {"agent": spec["agent"]}, {"environment": spec["environment"]},
            {"tenant_id": str(uuid.uuid4())}, {"Metadata": {}},
        ]
        for fields in invalid_fields:
            response = raw.post(url, headers=headers, json=fields)
            assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_request"
            expect_error(BadRequestError, lambda: sessions.update(first.id, extra_body=fields))
            assert sessions.retrieve(first.id) == current
        # Metadata values and limits report the official code, param and message.
        field_errors = [
            ({"key": None}, "metadata.key", "Invalid type for 'metadata.key': expected a string, but got null instead."),
            ({"key": 1}, "metadata.key", "Invalid type for 'metadata.key': expected a string, but got an integer instead."),
            ({"key": []}, "metadata.key", "Invalid type for 'metadata.key': expected a string, but got an array instead."),
            ({"key": {}}, "metadata.key", "Invalid type for 'metadata.key': expected a string, but got an object instead."),
            ({str(i): "value" for i in range(17)}, "metadata", "Invalid 'metadata': too many properties. Expected an object with at most 16 properties, but got an object with 17 properties instead."),
            ({"界" * 65: "value"}, "metadata." + "界" * 65, "Invalid property name in 'metadata': '" + "界" * 65 + "' is too long. Expected a string with maximum length 64, but got a string with length 65 instead."),
            ({"key": "🧪" * 513}, "metadata.key", "Invalid 'metadata.key': string too long. Expected a string with maximum length 512, but got a string with length 513 instead."),
            ({"key": "a\x00b"}, "metadata.key", "Invalid 'metadata.key': string contains U+0000, which this service cannot store."),
        ]
        for metadata, param, message in field_errors:
            response = raw.post(url, headers=headers, json={"metadata": metadata})
            assert response.status_code == 400 and response.json()["error"] == {
                "type": "invalid_request_error", "code": "invalid_request_error", "param": param, "message": message}
            error = expect_error(BadRequestError, lambda: sessions.update(first.id, metadata=metadata))
            assert error.body["param"] == param
            assert sessions.retrieve(first.id) == current
        # The shared body gate (HP-09..HP-15); a zero-length body or null is an
        # empty update, which still requires metadata (HP-13).
        official_body.check(raw, url, headers, official_body.rejected('{"metadata":{"k":"gate"}}', "k", "metadata.k"))
        json_headers = {**headers, **official_body.JSON}
        for body in ["", "null"]:
            response = raw.post(url, headers=json_headers, content=body)
            assert response.status_code == 400 and response.json()["error"]["message"] == "At least one update field is required"
        assert sessions.retrieve(first.id) == current
        oversized = json.dumps({"metadata": {"key": "x" * (1024 * 1024)}})
        response = raw.post(url, headers=json_headers, content=oversized)
        assert response.status_code == 413 and response.json()["error"]["code"] == "request_too_large"
        assert raw.patch(url, headers=headers, json={"metadata": {}}).status_code == 405
        empty = raw.post(url, headers=headers, json={})
        assert empty.status_code == 400
        for path in [url + "?tenant_id=other", url + "?unsupported=1"]:
            response = raw.post(path, headers=headers, json={})
            assert response.status_code == 400 and response.json() == empty.json()
        for target in [other, invalid]:
            expected_error = AuthenticationError if target is invalid else NotFoundError
            for fields in [{"metadata": None}, {"metadata": {"tenant_id": "untrusted"}}]:
                expect_error(expected_error, lambda: target.beta.agents.sessions.update(first.id, **fields))
        expect_error(NotFoundError, lambda: sessions.update(str(uuid.uuid4()), metadata={}))
        expect_error(BadRequestError, lambda: sessions.update(str(uuid.uuid4())))
        expect_error(BadRequestError, lambda: other.beta.agents.sessions.update(first.id))
        expect_error(AuthenticationError, lambda: invalid.beta.agents.sessions.update(first.id))
        for malformed in ("invalid-id", "sess_" + uuid.uuid4().hex, str(uuid.UUID(int=0))):
            missing = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions/" + str(uuid.uuid4()), headers=headers, json={"metadata": {}})
            response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions/" + malformed, headers=headers, json={"metadata": {}})
            assert missing.status_code == response.status_code == 404 and response.json() == missing.json()
            expect_error(NotFoundError, lambda: sessions.update(malformed, metadata={}))
        expect_error(BadRequestError, lambda: sessions.update(first.id, extra_headers={"OpenAI-Beta": ""}))
        assert sessions.retrieve(first.id) == current
    print("Session metadata: pinned SDK and raw HTTP replacement, clearing, omission, limits, authentication, tenant isolation, unchanged configuration and creation retry identity passed.")
    return current


def verify_active_session_metadata(client, session_id):
    sessions = client.beta.agents.sessions
    before = sessions.retrieve(session_id)
    assert before.status == "in_progress"
    updated = sessions.update(session_id, metadata={"label": "active execution"})
    assert updated.metadata == {"label": "active execution"}
    assert without_metadata(updated) == without_metadata(before)
    return updated
