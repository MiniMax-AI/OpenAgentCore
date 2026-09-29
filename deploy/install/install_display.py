"""Shared terminal presentation for the Core and node installers."""
import os
import sys
import textwrap


def color(message, code, stream=None):
    stream = stream or sys.stdout
    if stream.isatty() and "NO_COLOR" not in os.environ and os.environ.get("TERM") != "dumb":
        return f"\033[{code}m{message}\033[0m"
    return message


def step(message):
    print("\n" + color("==> " + message + "...", "36"), flush=True)


def heading(message):
    print("\n" + color(message, "1"))


def error(message):
    print(color("Installation failed: ", "31", sys.stderr) + message, file=sys.stderr, flush=True)


def paragraph(message):
    print(textwrap.fill(message, width=88, initial_indent="  ", subsequent_indent="  ",
                        break_long_words=False, break_on_hyphens=False))
