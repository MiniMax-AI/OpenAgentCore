"""Referenced source-file acceptance against our actual API, not an OpenAI proxy."""

import hashlib
from pathlib import Path
import tempfile

from openai import BadRequestError


def verify_source_download_denied(client, http, source_id):
    endpoint = str(client.base_url).rstrip("/") + "/files/" + source_id + "/content"
    response = http.get(endpoint, headers={"Authorization": "Bearer " + client.api_key})
    assert response.status_code == 400
    error = response.json()["error"]
    assert (error["type"], error["code"], error["param"]) == ("invalid_request_error", None, None)
    try:
        client.files.content(source_id)
    except BadRequestError as denied:
        assert denied.status_code == 400 and denied.body == error
    else:
        raise AssertionError("SDK allowed public user_data content download")


def verify_source_files(client, foreign, http, environment, directory, cases):
    base = str(client.base_url).rstrip("/")
    headers = {"Authorization": "Bearer " + client.api_key}
    other = {"Authorization": "Bearer " + foreign.api_key}
    copies, receipts = {}, []
    for index, (name, content) in enumerate(cases.items()):
        if index % 2:
            response = http.post(base + "/files", headers=headers,
                                 data={"purpose": "user_data"}, files={"file": (name, content)})
            assert response.status_code == 200, "Raw source upload failed"
            source = response.json()
        else:
            source = client.files.create(file=(name, content), purpose="user_data").to_dict()
        source_id = source["id"]
        endpoint = base + "/files/" + source_id
        assert set(source) == {"id", "object", "bytes", "created_at", "filename", "purpose", "status", "expires_at", "status_details"}
        assert source["object"] == "file" and source["bytes"] == len(content) and source["filename"] == name
        assert source["purpose"] == "user_data" and source["status"] == "processed"
        assert source["expires_at"] is None and source["status_details"] is None
        assert client.files.retrieve(source_id).to_dict() == source
        verify_source_download_denied(client, http, source_id)
        for method, suffix in (("GET", ""), ("GET", "/content"), ("DELETE", "")):
            response = http.request(method, endpoint + suffix, headers=other)
            assert response.status_code == 404 and "error" in response.json(), "Foreign source access succeeded"
            assert source_id not in response.text and name not in response.text
        path = directory + "/source-" + name
        receipt = client.beta.agents.environments.files.create(environment, type="file_id", file_id=source_id, path=path).to_dict()
        assert receipt == {"environment_id": environment, "object": "agent.environment.file", "path": path, "size_bytes": len(content)}
        assert client.files.delete(source_id).to_dict() == {"id": source_id, "object": "file", "deleted": True}
        for suffix in ("", "/content"):
            assert http.get(endpoint + suffix, headers=headers).status_code == 404
        response = http.post(base + "/agents/environments/" + environment + "/files",
                             headers={**headers, "OpenAI-Beta": "agents=v1"},
                             json={"type": "file_id", "file_id": source_id, "path": path})
        assert response.status_code == 404, "Deleted source copied again"
        copies[path] = len(content)
        receipts.append({"path": path, "size": len(content), "sha256": hashlib.sha256(content).hexdigest()})

    foreign_source = foreign.files.create(file=("foreign.bin", b"foreign-private"), purpose="user_data")
    try:
        response = http.post(base + "/agents/environments/" + environment + "/files",
                             headers={**headers, "OpenAI-Beta": "agents=v1"},
                             json={"type": "file_id", "file_id": foreign_source.id, "path": directory + "/foreign.bin"})
        assert response.status_code == 404, "Foreign source crossed project boundary"
    finally:
        foreign.files.delete(foreign_source.id)

    scratch = Path.home() / ".oac" / "tmp"
    scratch.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryFile(dir=scratch) as body:
        chunk = bytes(range(256)) * 1024
        digest = hashlib.sha256()
        for _ in range(2048):
            body.write(chunk)
            digest.update(chunk)
        body.seek(0)
        large = client.files.create(file=("512MiB.bin", body), purpose="user_data")
    try:
        assert large.bytes == 512 << 20
        verify_source_download_denied(client, http, large.id)
        response = http.post(base + "/agents/environments/" + environment + "/files",
                             headers={**headers, "OpenAI-Beta": "agents=v1"},
                             json={"type": "file_id", "file_id": large.id, "path": directory + "/too-large.bin"})
        assert response.status_code == 413, "Destination size bound bypassed"
    finally:
        client.files.delete(large.id)
    # This digest identifies the upload fixture; public download is denied and proves no byte round-trip.
    return copies, receipts, {"source_limit_bytes": 512 << 20, "large_source_fixture_sha256": digest.hexdigest(),
                              "public_source_download_denied": True,
                              "deleted_sources_unavailable": True, "foreign_source_rejected": True}
