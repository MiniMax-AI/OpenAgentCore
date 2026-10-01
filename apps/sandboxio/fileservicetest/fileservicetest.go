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

	mu    sync.Mutex
	svc   *fileservice.Service
	conns []net.Conn
}

// New serves the absolute directory dir. The file service sets the process umask to zero.
func New(dir string) (*Server, error) {
	svc, err := fileservice.New(dir)
	if err != nil {
		return nil, err
	}
	return &Server{dir: dir, id: sandboxwire.NewID(), svc: svc}, nil
}

// Dial opens a stream to the current service incarnation.
func (s *Server) Dial(context.Context) (io.ReadWriteCloser, error) {
	s.mu.Lock()
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
	go sandboxfs.Serve(context.Background(), server, s.svc, a)
	return client, nil
}

// Break closes every open stream, as a lost transport does.
func (s *Server) Break() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakLocked()
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
