// Command oac-daemon is the reverse-WebSocket worker that pairs a user
// machine with a OpenAgentCore server and exposes a local agent CLI
// subprocess as a connector_type=agent_daemon target. See
// apps/daemon/README.md for the subcommand spec.
package main

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "oac-daemon: %v\n", err)
		os.Exit(1)
	}
}
