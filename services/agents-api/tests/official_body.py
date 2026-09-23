"""Official errors of the shared Agents API JSON body gate (HP-09..HP-15)."""

PARSE = ("Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. "
         "(Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)")
UNICODE = ("Invalid body: encountered a unicode decode error when parsing this JSON value. "
           "Please check the value to ensure it is valid unicode.")
CONTENT_TYPE = "expected request with Content-Type: application/json"
JSON = {"Content-Type": "application/json"}


def duplicate(key, path):
    return f"Invalid body: duplicate JSON key '{key}' at '{path}'. Duplicate JSON keys are not supported."


def rejected(valid, key, path, value="gate"):
    """Cases that must write nothing. valid is a compact JSON object body that would
    write; it contains the string member key:value at the dotted object-key path."""
    body = valid.encode()
    member = f'"{key}":"{value}"'.encode()
    assert member in body, valid
    return [
        (JSON, body + b"x", PARSE),
        (JSON, body + b"{}", PARSE),
        (JSON, b"\xef\xbb\xbf" + body, PARSE),
        (JSON, b" \n ", PARSE),
        (JSON, body.replace(member, member[:-1] + b'\xff"', 1), UNICODE),
        (JSON, body.replace(member, f'"{key}":"first",'.encode() + member, 1), duplicate(key, path)),
        (JSON, b'"scan6"', "Invalid type: expected an object, but got a string instead."),
        (JSON, b"[]", "Invalid type: expected an object, but got an array instead."),
        ({}, body, CONTENT_TYPE),
        ({"Content-Type": "text/plain"}, body, CONTENT_TYPE),
        ({"Content-Type": "application/x-www-form-urlencoded"}, body, CONTENT_TYPE),
        ({}, b"", CONTENT_TYPE),
    ]


def check(http, url, headers, cases):
    for extra, content, message in cases:
        response = http.post(url, headers={**headers, **extra}, content=content)
        assert response.status_code == 400 and response.json()["error"] == {
            "type": "invalid_request_error", "code": "invalid_request_error", "param": None,
            "message": message}, (content[:80], response.status_code, response.text)
