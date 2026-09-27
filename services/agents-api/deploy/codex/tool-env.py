"""Managed native hook: transform Bash input, never execute untrusted code."""
import json
import os
from pathlib import Path
import shlex
import sys

try:
    if os.environ.get('OAC_RUNTIME_TOOL_ENV') != '1':
        print('{}')
        sys.exit(0)
    value = json.load(sys.stdin)
    command = value['tool_input']['command']
    if value.get('hook_event_name') != 'PreToolUse' or value.get('tool_name') != 'Bash' or not isinstance(command, str):
        raise ValueError('invalid hook input')
    if not Path('/environment/initialization/tool-env.sh').is_file():
        raise ValueError('missing tool environment')
    # POSIX sh does not accept eval --. A leading space prevents option parsing
    # while preserving the native shell, cwd and command text.
    rewritten = '. /environment/initialization/tool-env.sh && eval ' + shlex.quote(' ' + command)
    if os.environ.get('OAC_RUNTIME_SYSTEM_PACKAGES') == '1':
        rewritten = '/usr/bin/python3 -I -S /usr/local/bin/oac-tool-root ' + shlex.quote(command)
    print(json.dumps({'hookSpecificOutput': {'hookEventName': 'PreToolUse',
          'permissionDecision': 'allow', 'updatedInput': {
          'command': rewritten}}}))
except Exception:
    # Native exit 2 denies the tool. Never return a partial rewrite or input.
    print('Initialized tool configuration unavailable', file=sys.stderr)
    sys.exit(2)
