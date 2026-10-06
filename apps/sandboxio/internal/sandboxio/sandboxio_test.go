//go:build linux

package sandboxio

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/internal/processservice"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxnet"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// TestMain runs like oac-sandbox-io's main: the trampoline hook first, then a
// child subreaper whose one reap loop owns every child exit.
func TestMain(m *testing.M) {
	processservice.Init()
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		panic(err)
	}
	go processservice.Reap(context.Background())
	os.Exit(m.Run())
}

const wait = 10 * time.Second

// writeBootstrap writes a bootstrap file for a fresh resource.
func writeBootstrap(t *testing.T, linkURL, credential string) (string, sandboxlink.ResourceRef) {
	t.Helper()
	in := sandboxbootstrap.Input{Version: sandboxbootstrap.Version, LinkURL: linkURL, Credential: credential, Resource: sandboxbootstrap.Resource{
		TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), Kind: "allocation", ID: uuid.NewString(), Generation: 1}}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, in.Resource.Ref()
}

// start runs the service with the export world at root. The returned stop
// ends it as SIGTERM does and returns run's result.
func start(t *testing.T, bootstrap, root string, srv *sandboxlinktest.Server) (stop func() error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, bootstrap, options{root: root, tls: srv.TLS}) }()
	stop = sync.OnceValue(func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(wait):
			return errors.New("the service did not stop")
		}
	})
	t.Cleanup(func() { stop() })
	return stop
}

// open opens a service stream, retrying while no serve peer is connected.
func open(link *sandboxlink.AttachLink, o sandboxlink.Open) (sandboxlink.Stream, sandboxlink.Opened, error) {
	for deadline := time.Now().Add(wait); ; time.Sleep(20 * time.Millisecond) {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		s, opened, err := link.OpenService(ctx, o)
		cancel()
		if !errors.Is(err, sandboxlink.ServiceUnavailable) || time.Now().After(deadline) {
			return s, opened, err
		}
	}
}

func shell(script string) sp.ProcessSpec {
	return sp.ProcessSpec{Executable: []byte("sh"), Argv: [][]byte{[]byte("sh"), []byte("-c"), []byte(script)},
		Env: []sp.EnvVar{{Name: []byte("PATH"), Value: []byte("/usr/bin:/bin")}}, Cwd: []byte("/"), Umask: 0o022,
		IOMode: sp.IOPipes, Scope: sp.ScopePOSIXSession}
}

func next(t *testing.T, op *sp.Operation) sp.Event {
	t.Helper()
	select {
	case ev := <-op.Events():
		return ev
	case <-time.After(wait):
		t.Fatal("no event")
		return nil
	}
}

func TestServesEachProtocolThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	auth := sandboxlinktest.NewAuthority()
	srv := sandboxlinktest.StartRelay(t, auth)
	bootstrap, resource := writeBootstrap(t, srv.URL, "serve-credential")
	auth.AddServe([]byte("serve-credential"), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource})
	runtimeID := sandboxwire.NewID()
	auth.AddRuntime([]byte("runtime-credential"), runtimeID)
	// The grant lets Network streams reach only an echo listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		if c, err := ln.Accept(); err == nil {
			io.Copy(c, c)
			c.Close()
		}
	}()
	echo := ln.Addr().(*net.TCPAddr).AddrPort()
	o := sandboxlink.Open{Resource: resource, AttachmentID: sandboxwire.NewID(), SessionID: sandboxwire.NewID(),
		AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1, AttachGrant: []byte("grant")}
	auth.AddGrant(o.AttachGrant, sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: resource, SessionID: o.SessionID,
		AssignmentID: o.AssignmentID, AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceFile, sandboxlink.ServiceProcess, sandboxlink.ServiceNetwork}, Lease: time.Minute,
		Egress: []sandboxlink.EgressRule{{Prefix: netip.PrefixFrom(echo.Addr(), 32), PortFirst: echo.Port(), PortLast: echo.Port()}}})
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: srv.URL, TLS: srv.TLS, RuntimeID: runtimeID, Credential: []byte("runtime-credential")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	root := t.TempDir()
	stop := start(t, bootstrap, root, srv)

	// A file created through the world export is the file under its root.
	o.Service, o.Version = sandboxlink.ServiceFile, sandboxfs.Version
	stream, opened, err := open(link, o)
	if err != nil {
		t.Fatal(err)
	}
	files := sandboxfs.NewClient(stream)
	defer files.Close()
	attached, err := files.Attach(ctx, &sandboxfs.AttachRequest{Export: "world"})
	if err != nil {
		t.Fatal(err)
	}
	var handles sandboxfs.HandleIDs
	note := handles.Next()
	if _, err := files.Create(ctx, &sandboxfs.CreateRequest{Handle: note, Parent: attached.Root.Node, Name: []byte("note"), Mode: 0o644, Access: sandboxfs.AccessReadWrite, Exclusive: true}); err != nil {
		t.Fatal(err)
	}
	if w, err := files.Write(ctx, &sandboxfs.WriteRequest{Handle: note, Data: []byte("hello")}); err != nil || w.Written != 5 {
		t.Fatalf("write: %+v, %v", w, err)
	}
	if r, err := files.Read(ctx, &sandboxfs.ReadRequest{Handle: note, Size: 64}); err != nil || string(r.Data) != "hello" {
		t.Fatalf("read: %+v, %v", r, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "note")); err != nil || string(b) != "hello" {
		t.Fatalf("on disk: %q, %v", b, err)
	}

	// A process runs to completion, with its exit and the end of its output
	// reported as separate events.
	o.Service, o.Version = sandboxlink.ServiceProcess, sp.Version
	stream, _, err = open(link, o)
	if err != nil {
		t.Fatal(err)
	}
	procs := sp.NewClient(stream)
	defer procs.Close()
	described, err := procs.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := procs.Start(ctx, described.ServerInstanceID, sandboxwire.NewID(), shell("echo hi; exit 7"))
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	var exited *sp.ExitedEvent
	for outputClosed := false; exited == nil || !outputClosed; {
		switch ev := next(t, op).(type) {
		case sp.OutputEvent:
			output.Write(ev.Data)
		case sp.ExitedEvent:
			exited = &ev
		case sp.OutputClosedEvent:
			outputClosed = true
		}
	}
	if output.String() != "hi\n" || exited.Status != (sp.ExitStatus{Kind: sp.ExitCode, Code: 7}) {
		t.Fatalf("output %q, exit %+v", output.String(), exited.Status)
	}

	// A Network stream connects under the Bind's egress and carries bytes
	// both ways.
	o.Service, o.Version = sandboxlink.ServiceNetwork, sandboxnet.Version
	stream, _, err = open(link, o)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sandboxnet.Connect(ctx, stream, echo.Addr().String(), echo.Port(), wait)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo: %q, %v", got, err)
	}

	// Stopping the service terminates its live operations before it returns.
	op, _, err = procs.Start(ctx, described.ServerInstanceID, sandboxwire.NewID(), shell("echo $$; exec sleep 600"))
	if err != nil {
		t.Fatal(err)
	}
	var line strings.Builder
	for !strings.HasSuffix(line.String(), "\n") {
		if ev, ok := next(t, op).(sp.OutputEvent); ok {
			line.Write(ev.Data)
		}
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line.String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := unix.Kill(pid, 0); err != unix.ESRCH {
		t.Fatalf("process %d after stop: %v", pid, err)
	}

	// A restarted service is a new instance.
	start(t, bootstrap, root, srv)
	o.ExpectedServerInstanceID = opened.ServerInstanceID
	if _, _, err := open(link, o); !errors.Is(err, sandboxlink.InstanceChanged) {
		t.Fatalf("reopen after restart: %v, want InstanceChanged", err)
	}
}

// A refused serve credential ends the service with the relay's typed failure,
// and the message never carries the credential.
func TestRefusedCredentialEndsTheService(t *testing.T) {
	srv := sandboxlinktest.StartRelay(t, sandboxlinktest.NewAuthority())
	bootstrap, _ := writeBootstrap(t, srv.URL, "unknown-credential")
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	err := run(ctx, bootstrap, options{root: t.TempDir(), tls: srv.TLS})
	if !errors.Is(err, sandboxlink.AuthenticationFailed) || strings.Contains(err.Error(), "unknown-credential") {
		t.Fatalf("run: %v, want AuthenticationFailed without the credential", err)
	}
}

// A failure before serving names its step, and the message never carries the
// credential.
func TestStartupFailureNamesTheStep(t *testing.T) {
	valid, _ := writeBootstrap(t, "wss://relay.invalid/link", "serve-credential")
	invalid := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(invalid, []byte(`{"version":1,"credential":"serve-credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ bootstrap, root, want string }{
		{invalid, t.TempDir(), "read the bootstrap file: "},
		{valid, filepath.Join(t.TempDir(), "missing"), "start the file service: "},
	} {
		err := run(context.Background(), c.bootstrap, options{root: c.root})
		if err == nil || !strings.HasPrefix(err.Error(), c.want) || strings.Contains(err.Error(), "serve-credential") {
			t.Errorf("run: %v, want %q without the credential", err, c.want)
		}
	}
}
