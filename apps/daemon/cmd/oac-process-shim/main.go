// Command oac-process-shim runs a declared sandbox executable from a Session
// view through the process broker. See apps/daemon/internal/processshim.
package main

import (
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
)

func main() { os.Exit(processshim.Run(processshim.SocketPath)) }
