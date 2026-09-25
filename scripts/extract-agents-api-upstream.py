#!/usr/bin/env python3
"""Extract the pinned official Agents API route set and field names.

Run this with a Python environment where the OpenAI Python SDK version pinned in
contracts/agents-api/upstream.json is installed (``pip install openai==3.13.0``
from that commit). It reads only the installed SDK source and types:

    python3 scripts/extract-agents-api-upstream.py \
        contracts/agents-api/upstream.json contracts/agents-api

and writes ``upstream-routes.json`` and ``upstream-fields.json`` to the output
directory. The public contract covers the Beta Agents resources
(``openai.resources.beta.agents``), Files (``openai.resources.files``) and
Skills (``openai.resources.skills``); see docs/api/public-agent-api.md.

Routes come from each synchronous resource method's ``self._get``/``_post``/
``_delete``/``_get_api_list`` call, with path parameters normalised to ``{}``.
Field names come from the request params TypedDicts (``body=maybe_transform``),
the query params TypedDicts (``make_request_options(query=maybe_transform(...))``)
and the response models (``cast_to`` or ``page``) of the same calls, followed
through nested models, TypedDicts, lists and unions. JSON names use the SDK's
wire alias. Page wrappers are the SDK's hand-written classes, not generated
wire types.
"""

import ast
import importlib
import inspect
import json
import pkgutil
import re
import sys
import typing
from pathlib import Path

import openai
import pydantic
import typing_extensions
from openai._utils import PropertyInfo

RESOURCE_PACKAGES = ("openai.resources.beta.agents", "openai.resources.files", "openai.resources.skills")
HTTP_CALLS = {"_get": "GET", "_post": "POST", "_delete": "DELETE", "_put": "PUT", "_patch": "PATCH", "_get_api_list": "GET"}
PARAMETER = re.compile(r"\{[^}]*\}")


def modules():
    for name in RESOURCE_PACKAGES:
        package = importlib.import_module(name)
        yield package
        if hasattr(package, "__path__"):
            for info in pkgutil.walk_packages(package.__path__, name + "."):
                yield importlib.import_module(info.name)


def literal_path(node):
    if isinstance(node, ast.Call) and getattr(node.func, "id", None) == "path_template":
        node = node.args[0]
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return PARAMETER.sub("{}", node.value)
    if isinstance(node, ast.JoinedStr):
        return "".join(part.value if isinstance(part, ast.Constant) else "{}" for part in node.values)
    raise ValueError(f"unsupported path expression {ast.unparse(node)}")


def evaluate(node, namespace):
    return eval(compile(ast.Expression(node), "<sdk>", "eval"), namespace)  # SDK-owned names only


def is_model(value):
    return inspect.isclass(value) and (issubclass(value, pydantic.BaseModel) or typing_extensions.is_typeddict(value))


def type_id(value):
    generic = getattr(value, "__pydantic_generic_metadata__", None) or {}
    if generic.get("origin") is not None:
        items = sorted(type_id(item) for argument in generic["args"] for item in references(argument))
        return f"{type_id(generic['origin'])}[{' | '.join(items)}]"
    module = value.__module__.removeprefix("openai.").removeprefix("types.")
    return f"{module}.{value.__qualname__}"


def references(annotation):
    """Return nested model and TypedDict classes, unwrapping aliases and containers."""
    if is_model(annotation):
        return [annotation]
    origin = typing_extensions.get_origin(annotation)
    if origin is None or origin in (typing.Literal, typing_extensions.Literal):
        return []
    found = []
    arguments = typing_extensions.get_args(annotation)
    if origin in (typing.Annotated, typing_extensions.Annotated):
        arguments = arguments[:1]
    for argument in arguments:
        found.extend(value for value in references(argument) if value not in found)
    return found


def alias(annotation, name):
    if typing_extensions.get_origin(annotation) in (typing.Annotated, typing_extensions.Annotated):
        for metadata in typing_extensions.get_args(annotation)[1:]:
            if isinstance(metadata, PropertyInfo) and metadata.alias:
                return metadata.alias
    return name


def fields(value):
    if typing_extensions.is_typeddict(value):
        hints = typing_extensions.get_type_hints(value, include_extras=True)
        return {alias(annotation, name): annotation for name, annotation in hints.items()}
    return {field.alias or name: field.annotation for name, field in value.model_fields.items()}


def keyword_value(call, name):
    return next((keyword.value for keyword in call.keywords if keyword.arg == name), None)


def request_types(call, namespace, name="body"):
    """Return the params types transformed into the call's body, or its query."""
    value = keyword_value(call, "body") if name == "body" else None
    if name == "query":
        options = keyword_value(call, "options")
        if isinstance(options, ast.Call):
            value = keyword_value(options, "query")
    found = []
    for node in ast.walk(value) if value is not None else []:
        if isinstance(node, ast.Call) and getattr(node.func, "id", "").endswith("maybe_transform") and len(node.args) == 2:
            for candidate in ast.walk(node.args[1]):
                if isinstance(candidate, ast.Attribute):
                    found.extend(value for value in references(evaluate(candidate, namespace)) if value not in found)
    return found


def response_types(call, namespace):
    for keyword in call.keywords:
        if keyword.arg == "page":
            return [evaluate(keyword.value, namespace)]
        if keyword.arg == "cast_to":
            node = keyword.value
            if isinstance(node, ast.Call) and getattr(node.func, "id", None) == "cast":
                node = node.args[1]
            return references(evaluate(node, namespace))
    return []


def operations():
    found = {}
    for module in modules():
        tree = ast.parse(inspect.getsource(module))
        for resource in tree.body:
            if not isinstance(resource, ast.ClassDef) or not any(getattr(base, "id", None) == "SyncAPIResource" for base in resource.bases):
                continue
            for call in ast.walk(resource):
                if not (isinstance(call, ast.Call) and isinstance(call.func, ast.Attribute) and call.func.attr in HTTP_CALLS
                        and getattr(call.func.value, "id", None) == "self"):
                    continue
                route = f"{HTTP_CALLS[call.func.attr]} {literal_path(call.args[0])}"
                entry = found.setdefault(route, {"request": [], "query": [], "response": []})
                namespace = vars(module)
                for key, values in (("request", request_types(call, namespace)), ("query", request_types(call, namespace, "query")),
                                    ("response", response_types(call, namespace))):
                    entry[key].extend(value for value in values if value not in entry[key])
    return found


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: extract-agents-api-upstream.py UPSTREAM_JSON OUTPUT_DIRECTORY")
    pin = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    if openai.__version__ != pin["sdk_version"]:
        sys.exit(f"installed openai {openai.__version__} does not match pinned {pin['sdk_version']}")
    found = operations()
    header = {"sdk_version": pin["sdk_version"], "commit": pin["commit"], "generator": "scripts/extract-agents-api-upstream.py"}
    types, pending = {}, [value for entry in found.values() for key in ("request", "query", "response") for value in entry[key]]
    while pending:
        value = pending.pop()
        if type_id(value) in types:
            continue
        nested = {}
        for name, annotation in fields(value).items():
            children = references(annotation)
            nested[name] = sorted(type_id(child) for child in children)
            pending.extend(children)
        types[type_id(value)] = dict(sorted(nested.items()))
    output = Path(sys.argv[2])
    routes = {**header, "resources": list(RESOURCE_PACKAGES), "routes": sorted(found)}
    (output / "upstream-routes.json").write_text(json.dumps(routes, indent=2) + "\n", encoding="utf-8")
    operation_types = {route: {key: sorted(type_id(value) for value in entry[key]) for key in ("request", "query", "response")} for route, entry in sorted(found.items())}
    document = {**header, "operations": operation_types, "types": dict(sorted(types.items()))}
    (output / "upstream-fields.json").write_text(json.dumps(document, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
