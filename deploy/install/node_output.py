"""Completion guidance for a connected, ready Sandbox Provider node."""
import os
import shlex

from install_display import color, heading, paragraph


def summary(root, args, unit, account, system):
    print("\n" + color("Node installation complete.", "32"))
    heading("Status")
    print("  Core connection: connected")
    print("  Sandbox Provider: " + {"docker": "Docker", "microsandbox": "microsandbox"}[args.provider] + " (ready)")
    heading("Node")
    print("  Core: " + args.core_url)
    print("  Runs as: " + account)
    print("  State: " + str(root))
    heading("Manage")
    scope = "" if system else " --user"
    service = shlex.quote(unit)
    elevate = "sudo " if system and os.environ.get("SUDO_UID", "0") != "0" else ""
    print("  Status: " + elevate + "systemctl" + scope + " status " + service)
    print("  Logs: " + elevate + "journalctl" + scope + " -u " + service + " -f")
    heading("Next")
    paragraph("Open Nodes in Web to manage this host. No model request was made.")
    print(flush=True)
