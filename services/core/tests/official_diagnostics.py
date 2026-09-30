"""Bounded failure facts for the real-service official-client fixture."""

import json
import re
import subprocess
import sys


EVENTS = {
    "Core process configuration loaded from the process environment": "service_start",
    "oac-core startup failed": "service_exit",
}
ERRORS = {
    "context canceled": "context_canceled",
    "context deadline exceeded": "deadline_exceeded",
    "conn closed": "connection_closed",
    "invalid input": "invalid_input",
}
SQLSTATES = {
    "08006": "connection_failure", "22P02": "invalid_text_representation",
    "23503": "foreign_key_violation", "23505": "unique_violation",
    "23514": "check_violation", "40001": "serialization_failure",
    "40P01": "deadlock_detected", "57014": "query_canceled",
    "57P01": "admin_shutdown", "XX000": "database_internal_error",
}


def failure_facts(output, sensitive):
    """Project fixed labels only; arbitrary messages, fields and stack lines stay private."""
    events = []
    omitted = 0
    for line in output.splitlines()[-32:]:
        if len(line) > 16384 or any(value and value in line for value in sensitive):
            omitted += 1
            continue
        try:
            entry = json.loads(line)
        except (ValueError, RecursionError):
            omitted += 1
            continue
        if not isinstance(entry, dict) or not isinstance(entry.get("msg"), str) or entry["msg"] not in EVENTS:
            omitted += 1
            continue
        event = {"event": EVENTS[entry["msg"]]}
        error = entry.get("error")
        if isinstance(error, str):
            event["error"] = ERRORS.get(error, "unclassified")
            match = re.search(r"\(SQLSTATE ([0-9A-Z]{5})\)$", error)
            if match and match[1] in SQLSTATES:
                event.update(sqlstate=match[1], error=SQLSTATES[match[1]])
        events.append(event)
    return {"events": events, "omitted_lines": omitted}


def finish_server(process, log, sensitive, primary_error, stream=None):
    """Clean up without replacing the primary test failure with a cleanup failure."""
    exit_code = None
    cleanup_failed = False
    try:
        if process is not None:
            exit_code = process.poll()
            if exit_code is None:
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=15)
                    raise
        log.flush()
        log.seek(0)
        output = log.read()
        assert not any(value and value in output for value in sensitive), "Fixture secret leaked into the service log"
    except BaseException:
        if primary_error is None:
            raise
        cleanup_failed = True
    if primary_error is not None:
        try:
            facts = failure_facts(output, sensitive) if "output" in locals() else {"events": [], "log_unavailable": True}
            # null means the service was alive when failure cleanup began; do not
            # misreport our own SIGTERM as the cause of the test failure.
            facts.update(exit_code=exit_code, cleanup_failed=cleanup_failed)
            print("Official-client service failure: " + json.dumps(facts), file=stream or sys.stderr)
        except BaseException:
            pass  # Even a broken diagnostic stream must retain the primary failure.
