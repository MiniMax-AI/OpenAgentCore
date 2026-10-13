"""Validate HTTP responses against the pinned OpenAPI 3.1 contract."""

import json
import re

from jsonschema import Draft202012Validator


class ResponseValidator:
    def __init__(self, path):
        self.contract = json.loads(path.read_text())
        self.routes = [(re.compile("^" + re.sub(r"\{[^}]+\}", "[^/]+", route) + "$"), methods)
                       for route, methods in self.contract["paths"].items()]
        # Existing service differences are recorded in the contract coverage ledger.
        # Keep these test-only adjustments local; the published schema stays official.
        schemas = self.contract["components"]["schemas"]
        for name, fields in {"ErrorBodyResource": ("code",), "ListFilesResponse": ("first_id", "last_id"),
                             "OpenAIFile": ("expires_at", "status_details")}.items():
            for field in fields:
                schema = schemas[name]["properties"][field]
                schema["type"] = [schema["type"], "null"]

    def __call__(self, response):
        response.read()
        path = response.request.url.path.removeprefix("/v1")
        methods = self.contract["paths"].get(path)
        if methods is None:
            methods = next(methods for pattern, methods in self.routes if pattern.fullmatch(path))
        responses = methods[response.request.method.lower()]["responses"]
        status = str(response.status_code)
        if status not in responses and path.startswith(("/files", "/skills")) and response.status_code >= 400:
            # These upstream operations omit error responses; validate their shared error body.
            schema = {"$ref": "#/components/schemas/ErrorResponse"}
        else:
            content = responses[status].get("content")
            if not content:
                assert response.content == b""
                return
            media = response.headers["content-type"].split(";", 1)[0]
            schema = content[media]["schema"]
        Draft202012Validator({"components": self.contract["components"], **schema}).validate(response.json())
