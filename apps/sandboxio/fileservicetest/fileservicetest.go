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

// Server serves one attachment, whose state survives lost streams as it does behind Link.
type Server struct {
	dir string
	id  sandboxwire.ID

	mu      sync.Mutex
	svc     *fileservice.Service
	files   *sandboxfs.Server // serves svc, as wrapped, to every stream
	binds   uint64            // stands in for Link's bind sequence: streams are bound in Dial order
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
	s := &Server{dir: dir, id: sandboxwire.NewID(), svc: svc}
	s.serveLocked()
	return s, nil
}

// serveLocked starts serving the current incarnation, as wrapped, to later streams.
func (s *Server) serveLocked() {
	var svc sandboxfs.Service = s.svc
	if s.wrap != nil {
		svc = s.wrap(svc)
	}
	s.files = sandboxfs.NewServer(svc)
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
		Exports:          []sandboxlink.ExportGrant{{ID: sandboxfs.WorldExport}},
	}
	s.binds++
	go s.files.Serve(context.Background(), server, a, s.binds)
	return client, nil
}

// Intercept wraps the service each later stream serves, so a test can change what it answers. Streams dialed before and after it are not fenced against each other, so a test calls it before the first Dial.
func (s *Server) Intercept(wrap func(sandboxfs.Service) sandboxfs.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wrap = wrap
	if s.svc != nil {
		s.serveLocked()
	}
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
	if err != nil {
		s.svc = nil
		return err
	}
	s.svc = svc
	s.serveLocked()
	return nil
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
