"""Session cleanup for acceptance scripts that must not mask an earlier failure."""

import sys
import time

from openai import APIStatusError


def delete_session(sessions, session_id, timeout=60):
    """Delete an owned Session from a ``finally`` block.

    Core deletes only a durably idle or failed Session. When deletion returns the
    409 ``conflict_error`` for a busy Session, cancel once, poll until the Session
    is idle or failed without required actions, then delete once more. A cleanup
    failure is logged while another exception is propagating, so the original
    assertion stays visible, and raised otherwise.
    """
    original = sys.exc_info()[1]
    try:
        try:
            sessions.delete(session_id)
            return
        except APIStatusError as exc:
            if exc.status_code != 409 or getattr(exc, "code", None) != "conflict_error":
                raise
        sessions.events.create(session_id, events=[{"type": "agent.session.input.cancel"}])
        deadline = time.monotonic() + timeout
        while True:
            state = sessions.retrieve(session_id)
            if state.status in ("idle", "failed") and not state.required_actions:
                break
            if time.monotonic() >= deadline:
                break
            time.sleep(1)
        sessions.delete(session_id)
    except Exception as exc:
        if original is None:
            raise
        print(f"Session {session_id} cleanup failed after an earlier error: {exc!r}", file=sys.stderr)
