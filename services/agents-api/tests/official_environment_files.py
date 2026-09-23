"""Pinned Files.list checks for a known, unchanged directory of regular files."""

import base64
from pathlib import PurePosixPath
from urllib.parse import urlencode

from openai import BadRequestError, NotFoundError


EMPTY_PAGE = {"object": "page", "data": [], "next": None, "has_more": False}
PROVISIONING = "the hosted environment is still provisioning; wait until it is connected before accessing files"


def assert_invalid_request(response, message, param=None):
    """Official Environment Files validation fields (HE-16, 18, 35, 38, 39)."""
    assert response.status_code == 400, "Expected a 400 validation error"
    assert response.json() == {"error": {"type": "invalid_request_error", "code": "invalid_request_error",
                                         "message": message, "param": param}}, "Wrong validation error fields"


def verify_wire_page(value):
    """The official token page envelope (HE-32): has_more is true exactly when next is set."""
    assert isinstance(value, dict) and set(value) == {"object", "data", "next", "has_more"}, "Wrong page fields"
    assert value["object"] == "page" and type(value["has_more"]) is bool, "Wrong page envelope"
    assert value["has_more"] is (value["next"] is not None), "has_more disagrees with next"


def verify_file_page(value, environment_id, limit):
    assert isinstance(value, dict) and isinstance(value.get("data"), list), "Invalid file page"
    assert len(value["data"]) <= limit, "File page exceeds the requested limit"
    assert value.get("has_more") is None or type(value["has_more"]) is bool, "Invalid has_more"
    assert value.get("next") is None or isinstance(value["next"], str), "Invalid next token"
    for item in value["data"]:
        assert isinstance(item, dict), "Invalid file entry"
        assert item.get("environment_id") == environment_id, "Wrong file Environment"
        assert item.get("object") == "agent.environment.file", "Wrong file object"
        assert isinstance(item.get("path"), str) and item["path"].startswith("/"), "Invalid file path"
        assert type(item.get("size_bytes")) is int and item["size_bytes"] >= 0, "Invalid file size"
    more = value.get("has_more") is not False and bool(value.get("next"))
    assert value.get("has_more") is not True or more, "Missing continuation token"
    assert not more or value["data"], "Empty page cannot continue through the pinned SDK"
    return more


def verify_environment_files(client, http, environment_id, directory, expected):
    assert PurePosixPath(directory).is_absolute() and expected, "Expected directory and files required"
    assert all(str(PurePosixPath(path).parent) == directory for path in expected), "Use a flat fixture directory"
    resource = client.beta.agents.environments.files
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    summary, continuation = [], None
    for order, limit in (("asc", 1), ("desc", 2), (None, 2)):
        params = {"path": directory, "limit": limit}
        if order is not None:
            params["order"] = order
        wanted = sorted(expected, key=lambda path: PurePosixPath(path).parts, reverse=order != "asc")
        found, tokens, page_sizes = [], set(), []
        for _ in range(len(expected) + 1):
            response = http.get(endpoint, headers=headers, params=params)
            assert response.status_code == 200, "Raw Files.list failed"
            assert response.headers.get("content-type", "").startswith("application/json"), "Wrong file page content type"
            value = response.json()
            verify_wire_page(value)
            more = verify_file_page(value, environment_id, limit)
            found.extend(value["data"])
            page_sizes.append(len(value["data"]))
            if not more:
                break
            token = value["next"]
            assert token not in tokens, "Files.list repeated a continuation token"
            tokens.add(token)
            if order == "asc" and continuation is None:
                continuation = token
            params["page"] = token
        else:
            raise AssertionError("Files.list did not terminate")
        assert [item["path"] for item in found] == wanted, "Raw file order, filtering or completeness differs"
        assert {item["path"]: item["size_bytes"] for item in found} == expected, "Raw file sizes differ"

        params.pop("page", None)
        page = resource.list(environment_id, **params)
        sdk_files, sdk_tokens = [], set()
        for _ in range(len(expected) + 1):
            more = verify_file_page(page.to_dict(), environment_id, limit)
            sdk_files.extend(item.to_dict() for item in page.data)
            assert page.has_next_page() == more, "SDK continuation disagrees with the wire page"
            if not more:
                break
            assert page.next not in sdk_tokens, "SDK repeated a continuation token"
            sdk_tokens.add(page.next)
            page = page.get_next_page()
        else:
            raise AssertionError("SDK Files.list did not terminate")
        assert sdk_files == found, "SDK file pages differ from raw HTTP"
        summary.append({"order": order or "default", "limit": limit, "page_sizes": page_sizes,
                        "files": found})
    assert continuation, "The fixture must exercise continuation"
    return summary, continuation


def verify_file_tenant_isolation(client, other, http, environment_id, directory, page, private_paths):
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    foreign = {"Authorization": "Bearer " + other.api_key, "OpenAI-Beta": "agents=v1"}
    # Missing paths, repeated keys and invalid bodies never reveal a foreign Environment.
    response = http.post(endpoint, headers=foreign, json={"type": "inline", "data": "", "path": directory + "/x", "extra": 1})
    assert response.status_code == 404, "Foreign tenant can reach Files.create validation"
    response = http.get(endpoint + "?" + urlencode([("limit", "1"), ("limit", "2")]), headers=foreign)
    assert response.status_code == 404, "Foreign tenant can reach Files.list query validation"
    for params in ({"path": directory, "limit": 1, "order": "asc"},
                   {"path": directory, "limit": 1, "order": "asc", "page": page},
                   {"path": directory + "/missing-directory"}):
        response = http.get(endpoint, params=params, headers=foreign)
        assert response.status_code == 404, "Foreign tenant can access Files.list"
        assert isinstance(response.json().get("error"), dict), "Missing safe error envelope"
        assert all(secret not in response.text for secret in (
            client.api_key, other.api_key, environment_id, *private_paths)), "Foreign response exposes private data"
        try:
            other.beta.agents.environments.files.list(environment_id, **params)
        except NotFoundError:
            pass
        else:
            raise AssertionError("Foreign tenant can access SDK Files.list")


def verify_file_list_rows(client, http, environment_id, rows, empty_pages=True):
    """Replays Files.list rows F3-F8. rows: absolute directory, missing, file and optional symlink paths.

    empty_pages=False selects the documented exception for daemons without a local
    workspace binding: the Claude SDK adapter reader keeps 404 for a missing path
    and 503 for a regular file or symlink.
    """
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    resource = client.beta.agents.environments.files
    directory = rows["directory"]
    baseline = http.get(endpoint, headers=headers, params={"path": directory})
    assert baseline.status_code == 200, "Baseline Files.list failed"
    verify_wire_page(baseline.json())
    checked = []
    # F3: unknown keys are ignored.
    response = http.get(endpoint, headers=headers, params={"path": directory, "foo": "bar"})
    assert response.status_code == 200 and response.json() == baseline.json(), "Unknown query key changed the page"
    sdk = resource.list(environment_id, path=directory, extra_query={"foo": "bar"})
    assert [item.to_dict() for item in sdk.data] == baseline.json()["data"], "SDK unknown key changed the page"
    checked.append("unknown_key_ignored")
    # F4: a repeated supported key is rejected.
    for key, value in (("path", directory), ("limit", "1"), ("order", "asc"), ("page", "token")):
        response = http.get(endpoint + "?" + urlencode([(key, value), (key, value)]), headers=headers)
        assert_invalid_request(response, "Failed to deserialize query string: duplicate field `" + key + "`")
    checked.append("repeated_key_rejected")
    # F5/F6: paths that name no listable directory list nothing; links are not followed.
    for name in ("missing", "file", "symlink"):
        if name not in rows:
            continue
        response = http.get(endpoint, headers=headers, params={"path": rows[name]})
        if not empty_pages:
            expected = 404 if name == "missing" else 503
            assert response.status_code == expected and set(response.json()) == {"error"}, "Adapter reader result changed"
            checked.append(name + "_adapter_" + str(expected))
            continue
        assert response.status_code == 200 and response.json() == EMPTY_PAGE, "Non-directory path did not list empty"
        page = resource.list(environment_id, path=rows[name])
        assert page.data == [] and page.has_more is False and page.next is None and not page.has_next_page(), "SDK empty page"
        checked.append(name + "_empty_page")
    # F7 and F8: path and token validation errors.
    for params, message in (
        ({"path": directory + "/"}, "path must identify a non-reserved directory inside /workspace"),
        ({"path": directory + "/./x"}, "path must identify a non-reserved directory inside /workspace"),
        ({"path": directory.lstrip("/")}, "path must be an absolute directory inside /workspace"),
        ({"path": "/etc"}, "path must be an absolute directory inside /workspace"),
        ({"page": "garbage"}, "Invalid file page token for this request"),
    ):
        assert_invalid_request(http.get(endpoint, headers=headers, params=params), message)
    try:
        resource.list(environment_id, path=directory + "/")
    except BadRequestError as error:
        assert error.status_code == 400, "SDK trailing-slash status"
    else:
        raise AssertionError("SDK accepted a trailing-slash path")
    checked.append("path_and_token_errors")
    return checked


def verify_file_create_rows(client, http, environment_id, directory):
    """Replays Files.create rows F1 and F8 below an existing workspace directory."""
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    absolute = "environment.files[0].path must be an absolute POSIX path inside /workspace"
    components = "environment.files[0].path cannot contain empty, . or .. path components"
    for path, message in (
        ("relative.txt", absolute), ("/workspace", absolute), ("/tmp/outside.txt", absolute),
        ("/workspace/nul\u0000.txt", absolute), (directory + "/slash/", components),
        ("/workspace/../escape.txt", components), (directory + "//double.txt", components),
    ):
        response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "cg==", "path": path})
        assert_invalid_request(response, message)
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "eA==", "path": directory + "/extra.txt", "extra_field": 1})
    assert_invalid_request(response, "Unknown parameter: 'extra_field'.", "extra_field")
    path = directory + "/created-201.txt"
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "MjAx", "path": path})
    assert response.status_code == 201, "Files.create did not return 201"
    assert response.json() == {"environment_id": environment_id, "object": "agent.environment.file", "path": path, "size_bytes": 3}
    return path


def verify_file_write_rows(client, http, environment_id, directory):
    """Files.create write rows FW1-FW6: parent creation, no replacement and the inline bound."""
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    path = directory + "/n1/n2/nested.txt"
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "bmVzdGVk", "path": path})
    assert response.status_code == 201, "Files.create did not create missing parents"
    assert response.json() == {"environment_id": environment_id, "object": "agent.environment.file", "path": path, "size_bytes": 6}
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "b3RoZXI=", "path": path})
    assert_invalid_request(response, "environment.files paths must not traverse symlinks or overwrite existing files")
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": "cg==", "path": directory + "/n1"})
    assert_invalid_request(response, "file path conflicts with an existing environment file")
    oversized = base64.b64encode(b"x" * ((5 << 20) + 1)).decode()
    response = http.post(endpoint, headers=headers, json={"type": "inline", "data": oversized, "path": directory + "/oversized.bin"})
    assert_invalid_request(response, "environment.files[0].data exceeds the 5 MiB decoded limit")
    return path


def verify_files_provisioning(client, http, environment_id):
    """Files.list/create on a pending hosted Environment (F9). Returns None when it connected first."""
    endpoint = str(client.base_url).rstrip("/") + "/agents/environments/" + environment_id + "/files"
    headers = {"Authorization": "Bearer " + client.api_key, "OpenAI-Beta": "agents=v1"}
    before = client.beta.agents.environments.retrieve(environment_id).status
    listed = http.get(endpoint, headers=headers)
    created = http.post(endpoint, headers=headers, json={"type": "inline", "data": "cA==", "path": "/workspace/pending.txt"})
    after = client.beta.agents.environments.retrieve(environment_id).status
    if before != "pending" or after != "pending":
        return None
    assert_invalid_request(listed, PROVISIONING)
    assert_invalid_request(created, PROVISIONING)
    return {"list": listed.status_code, "create": created.status_code}
