"""Keep response validation strict when consuming the official OpenAPI format."""

import json
import subprocess
import tempfile
from pathlib import Path
import unittest

import httpx2
from jsonschema import Draft202012Validator, ValidationError
from official_schema import ResponseValidator


class ResponseSchemaTests(unittest.TestCase):
    def setUp(self):
        self.validator = ResponseValidator(Path(__file__).resolve().parents[3] / "contracts/agents-api/openapi.yaml")

    def response(self, path, body, status=200, method="GET"):
        response = httpx2.Response(status, json=body, request=httpx2.Request(method, "http://localhost/v1" + path))
        self.validator(response)

    def test_static_session_route_and_nullable_empty_page(self):
        body = {"object": "list", "data": [], "first_id": None, "last_id": None, "has_more": False}
        self.response("/agents/sessions", body)
        self.response("/files", body)
        with self.assertRaises(ValidationError):
            self.response("/files", {**body, "has_more": "false"})
        with self.assertRaises(ValidationError):
            self.response("/agents/sessions", {key: value for key, value in body.items() if key != "data"})

    def test_declared_and_shared_error_responses(self):
        body = {"error": {"message": "Invalid key", "type": "invalid_request_error", "code": None, "param": None}}
        self.response("/agents/agent_1", body, 401)
        self.response("/files/file_1", body, 404)
        with self.assertRaises(ValidationError):
            self.response("/files/file_1", {"error": "missing"}, 404)
        with self.assertRaises(KeyError):
            self.response("/agents/agent_1", body, 418)

    def test_file_nulls_do_not_relax_other_fields(self):
        body = {"id": "file_1", "object": "file", "bytes": 0, "created_at": 1, "filename": "test.txt",
                "purpose": "user_data", "status": "processed", "expires_at": None, "status_details": None}
        self.response("/files/file_1", body)
        with self.assertRaises(ValidationError):
            self.response("/files/file_1", {**body, "bytes": None})
        with self.assertRaises(ValidationError):
            self.response("/files/file_1", {**body, "purpose": "unknown"})

    def test_generated_union_serialization_matches_official_variants(self):
        root = Path(__file__).resolve().parents[3]
        artifacts = Path.home() / ".oac/tests"
        artifacts.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=artifacts) as directory:
            program = Path(directory) / "main.go"
            program.write_text('''package main
import ("encoding/json"; "os"; v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1")
func main() {
    query := "reference"
    values := []v1.WebSearchAction{
        {Type: "search", Query: &query}, {Type: "open_page", Query: &query},
        {Type: "find_in_page"}, {Type: "other", Query: &query},
    }
    if err := json.NewEncoder(os.Stdout).Encode(values); err != nil { panic(err) }
}
''')
            payload = subprocess.check_output(["go", "run", str(program)], cwd=root, timeout=120)
        official = json.loads((root / "contracts/agents-api/upstream/openapi.json").read_text())
        validator = Draft202012Validator({"components": official["components"], "$ref": "#/components/schemas/WebSearchActionResource"})
        for action in json.loads(payload):
            validator.validate(action)

    def test_declared_bodyless_response(self):
        response = httpx2.Response(202, request=httpx2.Request("POST", "http://localhost/v1/agents/sessions/session_1/events"))
        self.validator(response)
        with self.assertRaises(AssertionError):
            self.response("/agents/sessions/session_1/events", {}, 202, "POST")


if __name__ == "__main__":
    unittest.main()
