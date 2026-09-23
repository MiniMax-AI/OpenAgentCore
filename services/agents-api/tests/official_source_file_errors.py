"""Qualified Files errors through real Core HTTP/PostgreSQL and the pinned SDK."""

import importlib.metadata
import json
import secrets
import sys
from contextlib import ExitStack
from pathlib import Path

import httpx2
from openai import DefaultHttpxClient, NotFoundError, OpenAI

from official_source_files import verify_source_download_denied


def main():
    base, token, foreign = sys.argv[1:]
    pin = json.loads((Path(__file__).resolve().parents[3] / "contracts/agents-api/upstream.json").read_text())
    distribution = importlib.metadata.distribution("openai")
    assert distribution.version == pin["sdk_version"]
    assert json.loads(distribution.read_text("direct_url.json"))["vcs_info"]["commit_id"] == pin["commit"]
    with ExitStack() as cleanup:
        raw = cleanup.enter_context(httpx2.Client(trust_env=False, timeout=10))
        clients = [cleanup.enter_context(OpenAI(
            api_key=key, base_url=base + "/v1", max_retries=0,
            _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False),
        )) for key in (token, foreign)]
        owner, outsider = clients
        secret = "private-file-content-" + secrets.token_hex(16)
        file = owner.files.create(file=("private-owned-fixture.txt", secret.encode()), purpose="user_data")
        cleanup.callback(owner.files.delete, file.id)
        before = owner.files.retrieve(file.id).to_dict()
        missing = "file-" + secrets.token_hex(24)
        rejections = 0
        for operation, method, suffix, param in [
            ("retrieve", "GET", "", "id"),
            ("content", "GET", "/content", "id"),
            ("delete", "DELETE", "", "id"),
            ("list", "GET", "", "after"),
        ]:
            def path(file_id):
                return "/v1/files?after=" + file_id if operation == "list" else "/v1/files/" + file_id + suffix

            def invoke(client, file_id):
                return client.files.list(after=file_id) if operation == "list" else getattr(client.files, operation)(file_id)

            bodies = []
            for key, client, file_id in [(token, owner, missing), (foreign, outsider, file.id)]:
                response = raw.request(method, base + path(file_id), headers={"Authorization": "Bearer " + key})
                assert response.status_code == 404, (operation, response.status_code)
                assert response.headers["cache-control"] == "no-store"
                body = response.json()["error"]
                assert set(body) == {"type", "code", "param", "message"}
                assert (body["type"], body["code"], body["param"]) == ("invalid_request_error", None, param), body
                assert isinstance(body["message"], str) and body["message"]
                assert all(value not in response.text for value in (file.id, file.filename, secret, token, foreign))
                bodies.append(response.json())
                try:
                    invoke(client, file_id)
                except NotFoundError as error:
                    assert error.status_code == 404 and error.body == body
                else:
                    raise AssertionError("SDK accepted " + operation)
                rejections += 2
            assert bodies[0] == bodies[1], operation
            unauthorized = raw.request(method, base + path(file.id))
            assert unauthorized.status_code == 401
            assert all(value not in unauthorized.text for value in (file.id, file.filename, secret))
            assert owner.files.retrieve(file.id).to_dict() == before
            verify_source_download_denied(owner, raw, file.id)
        assert list(outsider.files.list()) == []
        tail = owner.files.list(after=file.id)
        assert tail.data == [] and tail.has_more is False
        assert [value.id for value in owner.files.list()] == [file.id]
        print(json.dumps({"result": "passed", "sdk_and_raw_rejections": rejections,
                          "operations": 4, "tenant_isolation": True, "postgres": True}))


if __name__ == "__main__":
    main()
