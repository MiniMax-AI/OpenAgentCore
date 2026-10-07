//go:build linux

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview/sessionviewtest"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// The view test needs root with CAP_SYS_ADMIN and CAP_NET_ADMIN, /dev/fuse and no AppArmor confinement. Run it in a throwaway container:
//
//	CGO_ENABLED=0 go test -c -o /tmp/gateway.test ./apps/daemon/internal/gateway
//	docker run --rm --cgroupns=private --cap-add SYS_ADMIN --cap-add NET_ADMIN --device /dev/fuse --security-opt apparmor=unconfined \
//	  -e OAC_TEST_SESSIONVIEW=1 -v /tmp/gateway.test:/t.test:ro debian:bookworm-slim /t.test -test.v
const (
	gateEnv      = "OAC_TEST_SESSIONVIEW"
	harnessEnv   = "OAC_GATEWAY_HARNESS"
	endpointsEnv = "OAC_GATEWAY_ENDPOINTS"
	externalEnv  = "OAC_GATEWAY_EXTERNAL"
	viewID       = 1000
)

// The test binary is also the Harness inside the view.
func TestMain(m *testing.M) {
	sessionview.Init()
	if os.Getenv(harnessEnv) != "" {
		os.Exit(runHarness())
	}
	os.Exit(m.Run())
}

func TestListenersExistOnlyInTheSession(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see the comment at the top of this file", gateEnv)
	}
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	// A server in the host namespace stands in for a service-origin MCP
	// server. The Harness reaches it only through its listener.
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "tools") })}
	go server.Serve(ln)
	defer server.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	cfg := Config{
		Model:       modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://127.0.0.1:1", APIKey: upstreamKey},
		MCP:         []agent.MCPBinding{{ConnectionOrigin: "service", ServerLabel: "tools", Transport: "http", ServerURL: "http://127.0.0.1:" + port + "/mcp"}},
		Prompt:      proto.PromptRequestPayload{DisableExecutionEnvironment: true},
		OpenNetwork: startSandbox(t).open,
		Proxy:       true,
	}
	// The Harness's environment is built before the view exists.
	eps, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(eps)

	world, err := fileservicetest.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer world.Close()
	harness := t.TempDir()
	if err := os.Chmod(harness, 0o755); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, filepath.Join(harness, "harness"))

	stop := func() {}
	defer func() { stop() }()
	v, err := sessionview.Start(context.Background(), sessionview.Spec{
		World:         worldfs.New(fileservicetest.Export, world.Dial).Serve,
		StagingParent: t.TempDir(),
		CgroupParent:  sessionviewtest.CgroupParent(t),
		Private:       []sessionview.PrivateDir{{Name: "harness", HostDir: harness, Exec: true}},
		Process: sessionview.Process{
			Path: "/.oac/harness/harness", Args: []string{"harness"}, Dir: "/", UID: viewID, GID: viewID, Stderr: os.Stderr,
			Env: []string{harnessEnv + "=1", endpointsEnv + "=" + string(encoded), externalEnv + "=" + net.JoinHostPort(hostAddress(t), port)},
		},
		Network: sessionview.Network{Setup: func(netns *os.File) (err error) {
			stop, err = Start(netns, cfg)
			return err
		}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (sessionview.Exit{}) {
		t.Fatalf("Wait = %+v, %v; output %s", exit, err, out)
	}
	var report map[string]string
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("Harness output %q: %v", out, err)
	}
	for _, name := range harnessChecks {
		if msg, ok := report[name]; !ok || msg != "" {
			t.Errorf("%s: %q", name, msg)
		}
	}

	// The listeners still serve the Session's namespace; the host's has none.
	for _, u := range []string{eps.Proxy, eps.Model, eps.MCP["tools"]} {
		parsed, _ := url.Parse(u)
		if c, err := net.DialTimeout("tcp", parsed.Host, time.Second); err == nil {
			c.Close()
			t.Errorf("%s is reachable from the host namespace", parsed.Host)
		}
	}
}

var harnessChecks = []string{"model listener", "MCP listener", "no direct route", "route through the proxy"}

// runHarness checks the network from inside the view and prints a report.
func runHarness() int {
	external := os.Getenv(externalEnv)
	var eps Endpoints
	if err := json.Unmarshal([]byte(os.Getenv(endpointsEnv)), &eps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	proxyURL, err := url.Parse(eps.Proxy)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	direct := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: wait}
	proxied := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: wait}
	answers := func(client *http.Client, u string, status int, body string) error {
		resp, err := client.Get(u)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status || (body != "" && string(got) != body) {
			return fmt.Errorf("%s answered %d %q", u, resp.StatusCode, got)
		}
		return nil
	}
	checks := map[string]func() error{
		// An undeclared route is answered by the listener itself.
		"model listener": func() error { return answers(direct, eps.Model+"/", http.StatusNotFound, "") },
		"MCP listener":   func() error { return answers(direct, eps.MCP["tools"], http.StatusOK, "tools") },
		"no direct route": func() error {
			c, err := net.DialTimeout("tcp", external, 2*time.Second)
			if err == nil {
				c.Close()
				return errors.New("connected without the proxy")
			}
			if !errors.Is(err, unix.ENETUNREACH) {
				return fmt.Errorf("dial: %v, want ENETUNREACH", err)
			}
			return nil
		},
		// The same address answers through the proxy, which connects from the sandbox.
		"route through the proxy": func() error { return answers(proxied, "http://"+external+"/", http.StatusOK, "tools") },
	}
	report := map[string]string{}
	for _, name := range harnessChecks {
		report[name] = ""
		if err := checks[name](); err != nil {
			report[name] = err.Error()
		}
	}
	json.NewEncoder(os.Stdout).Encode(report)
	return 0
}

// hostAddress returns a non-loopback IPv4 address of the host namespace.
func hostAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
			return ip.IP.String()
		}
	}
	t.Fatal("the host namespace has no non-loopback IPv4 address; run the container with a network")
	return ""
}

func copyExecutable(t *testing.T, dst string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
