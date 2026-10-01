package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
)

// proxy is the generic proxy. It serves CONNECT tunnels and absolute-form
// plain-HTTP requests, and connects only through a new Network stream to the
// sandbox for each connection. It rejects every other request form.
type proxy struct {
	open    func(context.Context) (sandboxlink.Stream, error)
	forward *httputil.ReverseProxy
}

func newProxy(open func(context.Context) (sandboxlink.Stream, error), sandbox *http.Transport) *proxy {
	p := &proxy{open: open}
	if sandbox != nil {
		// The request is relayed as the Harness addressed it; the reverse
		// proxy drops hop-by-hop headers, Proxy-Authorization among them. It
		// also drops query parameters it cannot parse, so the query is
		// restored as sent.
		p.forward = reverseProxy(sandbox, func(pr *httputil.ProxyRequest) {
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
		})
	}
	return p
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case p.open == nil:
		http.Error(w, "the Session has no sandbox network", http.StatusForbidden)
	case r.Method == http.MethodConnect && r.URL.Host != "" && r.URL.Path == "":
		p.tunnel(w, r)
	case r.Method != http.MethodConnect && r.URL.Scheme == "http" && r.URL.Host != "" && !strings.HasPrefix(r.RequestURI, "/"):
		p.forward.ServeHTTP(w, r)
	default:
		http.Error(w, "the proxy serves CONNECT and absolute-form http requests only", http.StatusBadRequest)
	}
}

// tunnel connects through the sandbox, answers 200 and then carries raw bytes.
func (p *proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitHostPort(r.URL.Host)
	if err != nil {
		http.Error(w, "invalid CONNECT authority", http.StatusBadRequest)
		return
	}
	remote, err := connectSandbox(r.Context(), p.open, host, port)
	if err != nil {
		status := statusOf(err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	client, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		remote.Reset()
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// Bytes the Harness sent after the request, such as a TLS ClientHello,
	// may already be buffered.
	pending, _ := rw.Reader.Peek(rw.Reader.Buffered())
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		remote.Reset()
		client.Close()
		return
	}
	splice(r.Context(), client, pending, remote)
}

// splice carries bytes both ways between the Harness's connection and the
// sandbox's. Each direction's orderly end reaches the other side as a
// half-close after every byte before it, and starts no timeout. An error on
// either side, or the end of ctx, aborts both.
func splice(ctx context.Context, client net.Conn, pending []byte, remote *sandboxnet.Conn) {
	var end sync.Once
	abort := func() {
		end.Do(func() {
			remote.Reset()
			if l, ok := client.(interface{ SetLinger(int) error }); ok {
				l.SetLinger(0)
			}
			client.Close()
		})
	}
	stop := context.AfterFunc(ctx, abort)
	defer stop()

	out := make(chan struct{})
	go func() {
		defer close(out)
		var err error
		if len(pending) > 0 {
			_, err = remote.Write(pending)
		}
		if err == nil {
			_, err = io.Copy(struct{ io.Writer }{remote}, struct{ io.Reader }{client})
		}
		if err == nil {
			err = remote.CloseWrite()
		}
		if err != nil {
			abort()
		}
	}()
	_, err := io.Copy(struct{ io.Writer }{client}, struct{ io.Reader }{remote})
	if err == nil {
		err = closeWrite(client)
	}
	if err != nil {
		abort()
	}
	<-out
	end.Do(func() {
		remote.Close()
		client.Close()
	})
}

func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}
