#!/usr/bin/python3 -I
"""Keep Claude's native Bash prefix from rewrapping the isolated MCP entry."""
import os
import shlex
import sys


MCP_ENTRY = ['/usr/bin/python3', '-I', '-S',
             '/usr/local/bin/agents-api-runtime-initialize', 'stdio']
TOOL_ROOT = '/usr/local/bin/agents-api-tool-root'


def invocation(command):
    try:
        args = shlex.split(command, comments=False)
    except ValueError:
        args = []
    if len(args) == 7 and args[:5] == MCP_ENTRY:
        # The existing isolated helper authorizes the frozen package/server.
        return args
    # Preserve the complete native Bash command, including its cwd receipt.
    return [TOOL_ROOT, command]


if __name__ == '__main__':
    if len(sys.argv) != 2:
        sys.exit(1)
    argv = invocation(sys.argv[1])
    os.execv(argv[0], argv)
