// Command processserve serves the Linux process service on a Unix socket for
// tests of its callers, such as apps/daemon/internal/processbroker. Every
// connection is one stream of the same attachment, so a caller that redials
// finds its operations again, as it would through a Link.
//
// Usage: processserve <socket path>. It prints "ready" once it listens.
package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/processservice"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func main() {
	processservice.Init()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "processserve:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: processserve <socket path>")
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return err
	}
	ctx := context.Background()
	go processservice.Reap(ctx)
	svc, err := processservice.New(processservice.DefaultConfig())
	if err != nil {
		return err
	}
	ln, err := net.Listen("unix", os.Args[1])
	if err != nil {
		return err
	}
	att := sp.Attachment{ID: sandboxwire.NewID()}
	fmt.Println("ready")
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go sp.Serve(ctx, c, att, svc)
	}
}
