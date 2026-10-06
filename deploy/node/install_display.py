"""Shared terminal presentation for the Core and node installers."""
import os
import contextlib
import sys
import textwrap
import threading
import time


def color(message, code, stream=None):
    stream = stream or sys.stdout
    if stream.isatty() and "NO_COLOR" not in os.environ and os.environ.get("TERM") != "dumb":
        return f"\033[{code}m{message}\033[0m"
    return message


def step(message):
    print("\n" + color("==> " + message + "...", "36"), flush=True)


@contextlib.contextmanager
def busy(message):
    """Show elapsed time for a quiet operation whose completion percentage is unknown."""
    stream = sys.stderr
    if not stream.isatty() or os.environ.get("TERM") == "dumb":
        yield
        return
    stopped = threading.Event()
    started = time.monotonic()

    def update():
        frames = "|/-\\"
        while not stopped.wait(0.5):
            try:
                elapsed = int(time.monotonic() - started)
                print(f"\r{frames[elapsed % 4]} {message} ({elapsed}s)", end="\033[K", file=stream, flush=True)
            except OSError:
                return

    worker = threading.Thread(target=update, daemon=True)
    worker.start()
    try:
        yield
    finally:
        stopped.set()
        worker.join()
        with contextlib.suppress(OSError):
            print("\r\033[K", end="", file=stream, flush=True)


def heading(message):
    print("\n" + color(message, "1"))


def error(message):
    print(color("Installation failed: ", "31", sys.stderr) + message, file=sys.stderr, flush=True)


def paragraph(message):
    print(textwrap.fill(message, width=88, initial_indent="  ", subsequent_indent="  ",
                        break_long_words=False, break_on_hyphens=False))
