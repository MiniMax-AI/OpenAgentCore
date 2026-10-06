//go:build linux

// Package sandboxio assembles the Sandbox I/O service: it reads the
// Provider's bootstrap, connects to the relay as the Link serve peer and
// serves the File, Process and Network protocols on the streams the relay
// binds.
// docs/sandbox-bootstrap.md describes the launch.
package sandboxio

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/fileservice"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/netservice"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/processservice"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// shutdownMargin is how long Shutdown waits past the grace limit for killed
// processes to be observed gone.
const shutdownMargin = 5 * time.Second

// Run serves the sandbox the bootstrap file at bootstrapPath names, with the
// export world rooted at "/", until ctx ends or the relay refuses the link
// for good. The caller has made the process a child subreaper running
// processservice.Reap. Run returns nil when ctx ended, an error naming the
// step and never the credential when it could not start, and otherwise the
// relay's *sandboxlink.Error.
func Run(ctx context.Context, bootstrapPath string) error {
	return run(ctx, bootstrapPath, options{root: "/"})
}

// options are the seams tests use: a temporary export root and a TLS
// configuration that trusts a test relay. Run uses "/" and the system roots.
type options struct {
	root string
	tls  *tls.Config
}

func run(ctx context.Context, bootstrapPath string, opt options) error {
	raw, err := runtimefs.ReadPrivatePath(bootstrapPath, sandboxbootstrap.MaxBytes)
	if err != nil {
		return fmt.Errorf("read the bootstrap file: %w", err)
	}
	in, err := sandboxbootstrap.Decode(raw)
	if err != nil {
		return fmt.Errorf("read the bootstrap file: %w", err)
	}
	procCfg := processservice.DefaultConfig()
	procs, err := processservice.New(procCfg)
	if err != nil {
		return fmt.Errorf("start the process service: %w", err)
	}
	files, err := fileservice.New(opt.root)
	if err != nil {
		return fmt.Errorf("start the file service: %w", err)
	}
	defer files.Close()
	fileServer := sandboxfs.NewServer(files)

	// down records that a link attempt failed since the last accepted Hello,
	// so each drop is logged once rather than on every reconnect attempt.
	var down atomic.Bool
	serveErr := sandboxlink.Serve(ctx, sandboxlink.ServeConfig{
		URL:        in.LinkURL,
		TLS:        opt.tls,
		Credential: []byte(in.Credential),
		Resource:   in.Resource.Ref(),
		// The File service checks each stream's binding against its own
		// incarnation, so the link announces that one.
		ServerInstanceID: files.InstanceID(),
		Services: []sandboxlink.ServiceHandler{
			{Service: sandboxlink.ServiceFile, Version: sandboxfs.Version, Serve: func(ctx context.Context, b sandboxlink.Bind, seq uint64, s sandboxlink.Stream) {
				fileServer.Serve(ctx, s, sandboxfs.Attachment{ID: b.AttachmentID, ServerInstanceID: b.ExpectedServerInstanceID, Lease: ctx, Exports: b.Exports}, seq)
			}},
			{Service: sandboxlink.ServiceProcess, Version: sandboxprocess.Version, Serve: func(ctx context.Context, b sandboxlink.Bind, _ uint64, s sandboxlink.Stream) {
				sandboxprocess.Serve(ctx, s, sandboxprocess.Attachment{ID: b.AttachmentID}, procs)
			}},
			{Service: sandboxlink.ServiceNetwork, Version: sandboxnet.Version, Serve: netservice.New().Handle},
		},
		OnConnected: func(sandboxlink.HelloAccepted) { down.Store(false) },
		OnDisconnected: func(err error) {
			if !down.Swap(true) {
				log.Printf("oac-sandbox-io: relay link ended, reconnecting: %v", err)
			}
		},
		OnAttachmentLost:     procs.AttachmentLost,
		OnAttachmentRestored: procs.AttachmentRestored,
		OnAttachmentClosed:   func(id sandboxwire.ID, _ sandboxlink.CloseReason) { procs.AttachmentRevoked(id) },
	})

	// Serve accepts no more streams and every handler has returned.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), procCfg.CancelGraceLimit+shutdownMargin)
	defer cancel()
	procs.Shutdown(shutdown)
	if ctx.Err() != nil {
		return nil
	}
	return serveErr
}
