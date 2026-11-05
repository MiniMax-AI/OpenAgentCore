"""Standalone Environment acceptance over Core HTTP, without execution."""

import json
import sys
from contextlib import ExitStack

import openai
from openai import DefaultHttpxClient, OpenAI

HOSTED = {"type": "openai_hosted"}


def error(call):
    try:
        call()
    except openai.APIStatusError as failure:
        body = failure.response.json()["error"]
        return failure.status_code, body["code"], body.get("param"), body["message"]
    raise AssertionError("request was accepted")


def main():
    settings = json.load(sys.stdin)
    with ExitStack() as stack:
        def sdk(key):
            return stack.enter_context(OpenAI(base_url=settings["base"] + "/v1", api_key=key, max_retries=0,
                _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False)))

        owner, foreign = sdk(settings["token"]), sdk(settings["foreign"])
        api = owner.beta.agents.environments

        env = api.create(environment=HOSTED, idempotency_key="prewarm-1")
        assert env.type == "openai_hosted" and env.status == "pending", env
        assert api.create(environment=HOSTED, idempotency_key="prewarm-1").id == env.id
        assert error(lambda: api.create(environment={**HOSTED, "env": {"A": "b"}}, idempotency_key="prewarm-1"))[:2] == (409, "idempotency_conflict")
        assert error(lambda: api.create(environment=HOSTED, vault_ids=["vault"]))[:3] == (400, "invalid_request_error", "vault_ids")
        assert api.create(environment=HOSTED, vault_ids=[]).id != env.id

        # A retry compares the request as sent, even after its Template is gone; keys run to 256 bytes.
        templates = api.templates
        template = templates.create(name="prewarm", env={"A": "b"})
        long_key = "k" * 256
        templated = {"type": "openai_hosted", "environment_template_id": template.id}
        kept = api.create(environment=templated, idempotency_key=long_key)
        templates.delete(template.id)
        assert api.create(environment=templated, idempotency_key=long_key).id == kept.id

        page = api.list(limit=1, order="asc")
        assert page.data[0].id == env.id and page.has_more, page
        assert {env.id, kept.id} <= {item.id for item in api.list().data}
        assert not foreign.beta.agents.environments.list().data
        assert error(lambda: foreign.beta.agents.environments.retrieve(env.id))[0] == 404

        # An unattached Environment has no workspace to serve.
        assert error(lambda: api.files.list(env.id))[:3] == (409, "conflict_error", None)

        session = {"agent": {"model": "fixture"}}
        assert error(lambda: owner.beta.agents.sessions.create(environment={**HOSTED, "environment_id": env.id, "env": {}}, **session))[:3] == (400, "invalid_request_error", "environment.env")
        assert error(lambda: owner.beta.agents.sessions.create(environment={**HOSTED, "environment_id": "short"}, **session))[2] == "environment.environment_id"
        assert error(lambda: foreign.beta.agents.sessions.create(environment={**HOSTED, "environment_id": env.id}, **session))[0] == 404

        first = owner.beta.agents.sessions.create(environment={**HOSTED, "environment_id": env.id}, **session)
        assert first.environment.id == env.id, first
        assert error(lambda: owner.beta.agents.sessions.create(environment={**HOSTED, "environment_id": env.id}, **session))[:4] == (
            409, "conflict_error", "environment.environment_id", "Environment is attached to another session.")
        assert api.retrieve(env.id).status == "pending"

        # Deleting the Session is the only detach; the Environment takes the next Session.
        owner.beta.agents.sessions.delete(first.id)
        second = owner.beta.agents.sessions.create(environment={**HOSTED, "environment_id": env.id}, **session)
        assert second.environment.id == env.id and second.id != first.id
        print(json.dumps({"environment_id": env.id, "session_id": second.id}))


main()
