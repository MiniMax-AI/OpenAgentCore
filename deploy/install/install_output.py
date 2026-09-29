"""Human-readable completion guidance for the Core/Web installer."""
import shlex

import sandbox_setup
import configuration
from install_display import color, heading, paragraph


def choose_where(mode):
    return ("on the Nodes page in Web" if mode == "all" else
            "through the Core management API (POST /core/v1/sandbox/deployment)")


def size(resources):
    memory = resources["memory_mib"]
    return f'{resources["cpus"]} CPUs, ' + (f"{memory // 1024} GiB" if memory % 1024 == 0 else f"{memory} MiB")


def sandbox_lines(config, selection, deployment, reachable):
    if selection is None:
        return [f"Sandboxes: none chosen. Choose a sandbox backend {choose_where(config['mode'])}."]
    if deployment is None:
        return []
    if selection["provider"] == "e2b":
        resources = (deployment.get("specification") or {}).get("resources") or {}
        built = f" ({size(resources)})" if {"cpus", "memory_mib"} <= set(resources) else ""
        return [f'Sandboxes: E2B template {selection["e2b"]["template"]}{built}. E2B runs them; no nodes are needed.']
    lines = [f'Sandboxes: {sandbox_setup.NAMES[selection["provider"]]}, Standard ({size(selection["resources"])}).']
    if selection["provider"] == "microsandbox":
        lines.append("Execution nodes need KVM (/dev/kvm). This host needs KVM only if you add it as a node.")
    if not reachable:
        lines.append("Before adding nodes, set public_url to a reachable HTTPS address in config.json, then run the Apply command below.")
    add = "in Web, open Nodes and choose Add node" if config["mode"] == "all" else "in a Web console paired with this Core, open Nodes and choose Add node"
    lines.append(f"Add nodes: {add}, then run the command on each execution host.")
    return lines


def summary(root, config, addresses, fresh, selection, deployment, reachable, incomplete):
    mode = config["mode"]
    status = ("Services are running; sandbox setup needs attention." if incomplete else
              "Installation complete." if fresh else "Installation settings checked. Use Status below to inspect service health.")
    print("\n" + color(status, "33" if incomplete else "32"))
    heading("Access")
    for address in addresses:
        print("  " + address)
    heading("Sign in" if mode != "core-only" else "Authentication")
    print(f"  Core key file: {root / 'secrets/core.key'}")
    if mode != "core-only":
        paragraph("Use this key to sign in to Web. Keep it private.")
    else:
        paragraph("Use this key for the Core management API. Keep it private.")
    heading("Next")
    if mode != "core-only":
        paragraph("Create a Project and its API key on the Projects and keys page.")
    else:
        paragraph("Create a Project and its API key through the Core management API:")
        local = " (local only)" if configuration.loopback_listener(config["host"]) else ""
        print(f'  {configuration.service_origin(config, "core")}/core/v1{local}')
    if fresh and mode != "web-only":
        for line in sandbox_lines(config, selection, deployment, reachable):
            paragraph(line)
    heading("Manage")
    print(f"  Settings: {root / 'config.json'}")
    command = shlex.quote(str(root / "oac"))
    for label, action in (("Apply settings", "apply"), ("Status", "status"), ("Start", "start"), ("Stop", "stop")):
        print(f"  {label}: {command} {action}")
    print("\nNo model request was made. Quickstart: docs/getting-started/quickstart.md", flush=True)
