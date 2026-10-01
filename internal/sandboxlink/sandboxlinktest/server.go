package sandboxlinktest

import (
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
)

// Server is a relay on an httptest TLS server.
type Server struct {
	// URL is the relay's wss:// URL.
	URL string
	// TLS trusts the server's certificate.
	TLS   *tls.Config
	Relay *relay.Relay
}

// StartRelay starts a relay for cfg and stops it when the test ends.
func StartRelay(t testing.TB, cfg relay.Config) *Server {
	t.Helper()
	rl, err := relay.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(rl)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return &Server{
		URL:   "wss://" + strings.TrimPrefix(srv.URL, "https://"),
		TLS:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Relay: rl,
	}
}
