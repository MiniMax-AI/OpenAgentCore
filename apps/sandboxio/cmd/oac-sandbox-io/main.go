//go:build linux

// Command oac-sandbox-io is the Sandbox I/O service, the one process a
// Sandbox Provider starts in a sandbox. docs/sandbox-bootstrap.md describes
// its launch.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/processservice"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/sandboxio"
)

func main() {
	// A process launch re-executes this binary as a trampoline; Init runs it.
	processservice.Init()

	flags := flag.NewFlagSet("oac-sandbox-io", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bootstrapFile := flags.String("bootstrap-file", "", "")
	if flags.Parse(os.Args[1:]) != nil || *bootstrapFile == "" || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: oac-sandbox-io --bootstrap-file <absolute path>")
		os.Exit(2)
	}

	// Reap is the process's only wait. As a child subreaper it also reaps
	// the orphaned descendants of operations.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		fail(&sandboxio.StartupError{Step: sandboxio.StepSubreaper, Err: err})
	}
	go processservice.Reap(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), unix.SIGTERM, unix.SIGINT)
	defer stop()
	if err := sandboxio.Run(ctx, *bootstrapFile); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "oac-sandbox-io: %v\n", err)
	os.Exit(1)
}
