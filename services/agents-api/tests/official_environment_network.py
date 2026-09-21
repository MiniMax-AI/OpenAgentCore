"""Restricted-policy assertions for a real standalone Core and fixed SDK.

These checks establish resource behavior only. Native model/tool enforcement,
Files/Artifacts, cancellation and recovery need the real deployment driver.
"""

NETWORK = {'access': 'restricted', 'allowed_domains': ['Example.com', 'httpbingo.org', 'example.com']}


def verify_network_resources(client, foreign, http, agent):
    templates = client.beta.agents.environments.templates
    root = str(client.base_url).rstrip('/')
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    template = templates.create(network=NETWORK)
    endpoint = root + '/agents/environments/templates/' + template.id
    try:
        assert template.network.to_dict() == NETWORK
        assert templates.update(template.id, name='Network policy').network.to_dict() == NETWORK
        assert http.get(endpoint, headers=headers).json()['network'] == NETWORK
        assert next(item for item in templates.list() if item.id == template.id).network.to_dict() == NETWORK
        foreign_headers = {**headers, 'Authorization': 'Bearer ' + foreign.api_key}
        for method in ['GET', 'POST', 'DELETE']:
            response = http.request(method, endpoint, headers=foreign_headers,
                                    json={'network': {'access': 'enabled'}} if method == 'POST' else None)
            assert response.status_code == 404
        for network in [
            {'access': 'restricted'}, {'access': 'restricted', 'allowed_domains': []},
            {'access': 'restricted', 'allowed_domains': None},
            {'access': 'restricted', 'allowed_domains': ['*.example.com']},
            {'access': 'restricted', 'allowed_domains': ['https://example.com']},
            {'access': 'restricted', 'allowed_domains': ['127.0.0.1']},
            {'access': 'restricted', 'allowed_domains': ['example.com'] * 101},
            {'access': 'enabled', 'allowed_domains': ['example.com']},
        ]:
            for target in [endpoint, root + '/agents/environments/templates']:
                assert http.post(target, headers=headers, json={'network': network}).status_code == 400
            assert templates.retrieve(template.id).network.to_dict() == NETWORK
        for override in [{'access': 'enabled'}, {'access': 'restricted', 'allowed_domains': ['sub.example.com']},
                         {'access': 'restricted', 'allowed_domains': ['example.org']}]:
            response = http.post(root + '/agents/sessions', headers=headers, json={
                'agent': agent, 'environment': {'type': 'openai_hosted',
                'environment_template_id': template.id, 'network': override}})
            assert response.status_code == 400
        response = http.post(root + '/agents/sessions', headers=foreign_headers, json={
            'agent': agent, 'environment': {'type': 'openai_hosted', 'environment_template_id': template.id}})
        assert response.status_code == 404
        replacement = {'access': 'restricted', 'allowed_domains': ['httpbingo.org']}
        assert templates.update(template.id, network=replacement).network.to_dict() == replacement
        assert templates.update(template.id, network=None).network.to_dict() == {'access': 'enabled', 'allowed_domains': []}
        assert templates.update(template.id, network={'access': 'disabled'}).network.to_dict() == {'access': 'disabled', 'allowed_domains': []}
    finally:
        templates.delete(template.id)


def attach_network(client, environment, template_id=None):
    if template_id:
        ceiling = {'access': 'restricted', 'allowed_domains': ['example.com', 'httpbingo.org', 'example.org']}
        client.beta.agents.environments.templates.update(template_id, network=ceiling)
    return {**environment, 'network': NETWORK}


def verify_network_snapshot(client, http, session, spec, key):
    assert session.environment.network.to_dict() == NETWORK
    sessions = client.beta.agents.sessions
    assert sessions.retrieve(session.id).environment.network.to_dict() == NETWORK
    retried = sessions.create(**spec, extra_headers={'Idempotency-Key': key})
    assert retried.id == session.id and retried.environment.network.to_dict() == NETWORK
    changed = {**spec, 'environment': {**spec['environment'], 'network': {
        'access': 'restricted', 'allowed_domains': ['httpbingo.org', 'example.com']}}}
    # Equal effective authority does not erase the caller's original array identity.
    response = http.post(str(client.base_url).rstrip('/') + '/agents/sessions', json=changed,
                         headers={'Authorization': 'Bearer ' + client.api_key,
                                  'OpenAI-Beta': 'agents=v1', 'Idempotency-Key': key})
    assert response.status_code == 409
