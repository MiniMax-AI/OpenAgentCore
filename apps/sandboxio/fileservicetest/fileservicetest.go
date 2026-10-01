//go:build linux

// Package fileservicetest serves the Linux file service over a directory through in-memory streams, for testing File clients outside apps/sandboxio.
package fileservicetest

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/fileservice"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Export is the export the service serves.
const Export = fileservice.Export

// Server serves one attachment, whose state survives lost streams as it does behind Link.
type Server struct {
	dir string
	id  sandboxwire.ID

	mu      sync.Mutex
	svc     *fileservice.Service
	conns   []net.Conn
	stalled bool
	wrap    func(sandboxfs.Service) sandboxfs.Service
}

// New serves the absolute directory dir. The file service sets the process umask to zero.
func New(dir string) (*Server, error) {
	svc, err := fileservice.New(dir)
	if err != nil {
		return nil, err
	}
	return &Server{dir: dir, id: sandboxwire.NewID(), svc: svc}, nil
}

// Dial opens a stream to the current service incarnation. While the server is stalled it waits for ctx to end.
func (s *Server) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	s.mu.Lock()
	if s.stalled {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	defer s.mu.Unlock()
	if s.svc == nil {
		return nil, errors.New("fileservicetest: closed")
	}
	client, server := net.Pipe()
	s.conns = append(s.conns, server)
	a := sandboxfs.Attachment{
		ID:               s.id,
		ServerInstanceID: s.svc.InstanceID(),
		Lease:            context.Background(),
		Exports:          []sandboxlink.ExportGrant{{ID: Export}},
	}
	var svc sandboxfs.Service = s.svc
	if s.wrap != nil {
		svc = s.wrap(svc)
	}
	go sandboxfs.Serve(context.Background(), server, svc, a)
	return client, nil
}

// Intercept wraps the service each later stream serves, so a test can change what it answers.
func (s *Server) Intercept(wrap func(sandboxfs.Service) sandboxfs.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wrap = wrap
}

// Break closes every open stream, as a lost transport does.
func (s *Server) Break() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakLocked()
}

// Stall closes every open stream and leaves the service unreachable: every later Dial waits for its context to end.
func (s *Server) Stall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakLocked()
	s.stalled = true
}

func (s *Server) breakLocked() {
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

// Restart replaces the service with a new incarnation over the same directory and closes every open stream.
func (s *Server) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakLocked()
	if s.svc != nil {
		s.svc.Close()
	}
	svc, err := fileservice.New(s.dir)
	s.svc = svc
	return err
}

// Close closes every stream and the service.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakLocked()
	if s.svc == nil {
		return nil
	}
	err := s.svc.Close()
	s.svc = nil
	return err
}
