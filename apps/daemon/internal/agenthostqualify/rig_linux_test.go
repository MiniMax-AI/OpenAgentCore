//go:build linux

package agenthostqualify

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/codex"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/mcode"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agenthost"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

const (
	// manifest is the agent-host image's record of its Harness installations.
	manifest = "/opt/oac/harnesses.json"
	// proxyEnv names the HTTP proxy of a host whose only egress it is.
	proxyEnv = "OAC_QUALIFY_PROXY"
	// sandboxUser is the user the sandbox image runs oac-sandbox-io as.
	sandboxUser = 1000
)

// TestViewCgroupDelegation checks the cgroup v2 delegation of the agent-host
// container. Open refuses the container's own cgroup, which Docker mounts
// read-only, as unsupported. In a delegated parent, Open ends a view cgroup
// left behind with cgroup.kill and removes it.
func TestViewCgroupDelegation(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test with scripts/qualify-agent-host.sh", gateEnv)
	}
	cfg := agenthost.Config{StateDir: t.TempDir(), ViewCgroups: "/sys/fs/cgroup", UIDs: agenthost.UIDRange{First: 70000, Count: 8}, RelayURL: "ws://127.0.0.1:1",
		RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime-credential"), Harnesses: agent.NewRegistry(), Shim: shim, CADir: caDir}
	if h, err := agenthost.Open(cfg); err == nil {
		h.Close()
		t.Fatalf("Open accepted the undelegated %s", cfg.ViewCgroups)
	} else if !errors.Is(err, agenthost.ErrUnsupported) || !errors.Is(err, sessionview.ErrCgroup) {
		t.Fatalf("Open in the undelegated %s: %v, want ErrUnsupported from ErrCgroup", cfg.ViewCgroups, err)
	} else {
		t.Logf("without delegation: %v", err)
	}

	cfg.StateDir, cfg.ViewCgroups = t.TempDir(), sessionviewtest.CgroupParent(t)
	left := filepath.Join(cfg.ViewCgroups, "left")
	if err := os.Mkdir(left, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(left)
	if err != nil {
		t.Fatal(err)
	}
	sleep := exec.Command("sleep", "600")
	sleep.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(dir.Fd())}
	err = sleep.Start()
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer sleep.Process.Kill()
	h, err := agenthost.Open(cfg)
	if err != nil {
		t.Fatalf("Open in the delegated %s: %v", cfg.ViewCgroups, err)
	}
	h.Close()
	if err := sleep.Wait(); sleep.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Errorf("the process left in a view cgroup ended with %v, want SIGKILL from cgroup.kill", err)
	}
	if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open kept the view cgroup left behind: %v", err)
	}
}

// activate gives each Harness's discovery the environment that the agent-host
// image's manifest activates.
func activate(t *testing.T) {
	env, err := agent.ManifestEnvironment(manifest, claudesdk.Installation(), codex.Installation(), mcode.Installation())
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range env {
		t.Setenv(name, value)
	}
}

// testCA returns a certificate for 127.0.0.1 that is its own CA. The relay
// serves it, and the agent host and the sandbox trust only it.
func testCA(t *testing.T) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenAgentCore qualification CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tunnel serves the host of each OAC_QUALIFY_<KIND> base_url on a loopback
// address that /etc/hosts gives its name, and carries each connection through
// the HTTP proxy in OAC_QUALIFY_PROXY, because the gateway dials model
// providers directly. TLS stays end to end. Without the proxy it does nothing.
func tunnel(t *testing.T) {
	raw := os.Getenv(proxyEnv)
	if raw == "" {
		return
	}
	proxy, err := url.Parse(raw)
	if err != nil || proxy.Scheme != "http" || proxy.Host == "" {
		t.Fatalf("%s is not an http:// proxy URL", proxyEnv)
	}
	hosts, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer hosts.Close()
	served := map[string]bool{}
	for _, declaration := range harnesses {
		var options struct {
			ModelProvider struct {
				BaseURL string `json:"base_url"`
			} `json:"model_provider"`
		}
		// sessionOptions reports options that do not decode.
		_ = json.Unmarshal([]byte(os.Getenv("OAC_QUALIFY_"+strings.ToUpper(declaration.Info.Kind))), &options)
		base, err := url.Parse(options.ModelProvider.BaseURL)
		if err != nil || base.Scheme != "https" || served[base.Hostname()] {
			continue
		}
		served[base.Hostname()] = true
		port := base.Port()
		if port == "" {
			port = "443"
		}
		address := fmt.Sprintf("127.0.0.%d", 76+len(served))
		ln, err := net.Listen("tcp", net.JoinHostPort(address, port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		if _, err := fmt.Fprintf(hosts, "%s %s\n", address, base.Hostname()); err != nil {
			t.Fatal(err)
		}
		go forward(ln, proxy.Host, net.JoinHostPort(base.Hostname(), port))
	}
}

// forward carries each connection to ln through an HTTP CONNECT to target.
func forward(ln net.Listener, proxy, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			p, err := net.Dial("tcp", proxy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tunnel to %s: %v\n", target, err)
				return
			}
			defer p.Close()
			fmt.Fprintf(p, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
			r := bufio.NewReader(p)
			resp, err := http.ReadResponse(r, &http.Request{Method: http.MethodConnect})
			if err != nil || resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "tunnel to %s: CONNECT: %v %v\n", target, err, resp)
				return
			}
			go func() {
				io.Copy(p, c)
				p.(*net.TCPConn).CloseWrite()
			}()
			io.Copy(c, r)
		}()
	}
}
