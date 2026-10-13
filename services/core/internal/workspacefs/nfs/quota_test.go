package nfs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"golang.org/x/crypto/ssh"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/quota/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(raw)
}

func newKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	return signer, string(pem.EncodeToMemory(block))
}

// fakeHelper is an SSH server standing in for the forced helper command.
type fakeHelper struct {
	t        *testing.T
	address  string
	hostKey  string
	mu       sync.Mutex
	requests [][]byte
	respond  func([]byte) []byte
}

func startHelper(t *testing.T, client ssh.PublicKey, respond func([]byte) []byte) *fakeHelper {
	t.Helper()
	host, _ := newKey(t)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), client.Marshal()) {
			return nil, errors.New("unauthorized")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	f := &fakeHelper{t: t, address: listener.Addr().String(), hostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey()))), respond: respond}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, config)
		}
	}()
	return f
}

func (f *fakeHelper) serve(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()
	_, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	for candidate := range channels {
		channel, requests, err := candidate.Accept()
		if err != nil {
			return
		}
		go func() {
			for request := range requests {
				_ = request.Reply(request.Type == "exec", nil)
				if request.Type != "exec" {
					continue
				}
				input, _ := io.ReadAll(channel)
				f.mu.Lock()
				f.requests = append(f.requests, input)
				f.mu.Unlock()
				output := f.respond(input)
				if output == nil {
					time.Sleep(time.Minute)
				}
				_, _ = channel.Write(output)
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				channel.Close()
			}
		}()
	}
}

func (f *fakeHelper) received() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.requests...)
}

func quotaConfiguration(t *testing.T, address, hostKey string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"root": "/nfs/workspaces", "namespace_id": "22222222-2222-4222-8222-222222222222", "uid": 65532,
		"quota": map[string]string{"endpoint": address, "user": "oac-quota", "host_key": hostKey}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func credentialInput(key string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"ssh_private_key": key})
	return raw
}

func quotaControl(t *testing.T, parameters json.RawMessage, credential json.RawMessage) *Filesystem {
	t.Helper()
	canonical, encoded, err := Registration{}.Normalize(parameters, credential)
	if err != nil {
		t.Fatal(err)
	}
	control, err := Registration{}.Control(workspacefs.ConfigurationRecord{Configuration: workspacefs.Configuration{ID: "11111111-1111-4111-8111-111111111111", Adapter: Name, Parameters: canonical}, Credential: encoded})
	if err != nil {
		t.Fatal(err)
	}
	return control.(*Filesystem)
}

var fixtureReference = workspacefs.Reference{TenantID: "33333333-3333-4333-8333-333333333333", EnvironmentID: "44444444-4444-4444-8444-444444444444", ObjectID: "55555555-5555-4555-8555-555555555555"}

func TestQuotaConfigurationCodec(t *testing.T) {
	_, key := newKey(t)
	host, _ := newKey(t)
	hostKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))
	plain := json.RawMessage(`{"root":"/nfs/workspaces","namespace_id":"22222222-2222-4222-8222-222222222222","uid":65532}`)
	quota := quotaConfiguration(t, "storage.internal:22", hostKey+" root@storage")
	for name, input := range map[string][2]json.RawMessage{
		"quota without credential":  {quota, nil},
		"credential without quota":  {plain, credentialInput(key)},
		"null credential":           {quota, json.RawMessage(`null`)},
		"unknown credential field":  {quota, json.RawMessage(`{"ssh_private_key":"x","password":"y"}`)},
		"invalid private key":       {quota, credentialInput("not a key")},
		"host key options":          {quotaConfiguration(t, "storage.internal:22", `restrict `+hostKey), credentialInput(key)},
		"endpoint without port":     {quotaConfiguration(t, "storage.internal", hostKey), credentialInput(key)},
		"zero port":                 {quotaConfiguration(t, "storage.internal:0", hostKey), credentialInput(key)},
		"nested unknown parameter":  {json.RawMessage(strings.Replace(string(quota), `"user"`, `"identity_file":"/key","user"`, 1)), credentialInput(key)},
		"nested null parameter":     {json.RawMessage(strings.Replace(string(quota), `"user":"oac-quota"`, `"user":null`, 1)), credentialInput(key)},
		"explicit null quota":       {json.RawMessage(strings.Replace(string(plain), `}`, `,"quota":null}`, 1)), nil},
		"user with shell character": {json.RawMessage(strings.Replace(string(quota), `oac-quota`, `oac;quota`, 1)), credentialInput(key)},
	} {
		if _, _, err := (Registration{}).Normalize(input[0], input[1]); !errors.Is(err, workspacefs.ErrInvalid) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	canonical, encoded, err := Registration{}.Normalize(quota, credentialInput(key))
	if err != nil || strings.Contains(string(canonical), "root@storage") || strings.Contains(string(canonical), "PRIVATE") {
		t.Fatalf("canonical parameters %s %v", canonical, err)
	}
	again, reencoded, err := Registration{}.Normalize(canonical, encoded)
	if err != nil || !bytes.Equal(again, canonical) || !bytes.Equal(reencoded, encoded) {
		t.Fatal("encoded credential does not encode to itself", err)
	}
	if _, _, err := (Registration{}).Normalize(plain, nil); err != nil {
		t.Fatal("existing configuration without credential rejected", err)
	}
	config := workspacefs.Configuration{ID: "11111111-1111-4111-8111-111111111111", Adapter: Name, Parameters: canonical}
	if _, err := (Registration{}).Control(workspacefs.ConfigurationRecord{Configuration: config}); !errors.Is(err, workspacefs.ErrInvalid) {
		t.Fatal("quota Control constructed without credential", err)
	}
	control, err := Registration{}.Control(workspacefs.ConfigurationRecord{Configuration: config, Credential: encoded})
	if err != nil || !control.Declaration().CapacityQuota {
		t.Fatal("quota declaration", err)
	}
	resolver, err := Registration{}.Resolver(config)
	if err != nil || resolver.(*Filesystem).quota != nil {
		t.Fatal("resolver requires or holds a credential", err)
	}
	plainControl, err := Registration{}.Control(workspacefs.ConfigurationRecord{Configuration: workspacefs.Configuration{ID: config.ID, Adapter: Name, Parameters: plain}})
	if err != nil || plainControl.Declaration().CapacityQuota {
		t.Fatal("plain declaration", err)
	}
	if _, err := (Registration{}).Control(workspacefs.ConfigurationRecord{Configuration: workspacefs.Configuration{ID: config.ID, Adapter: Name, Parameters: plain}, Credential: encoded}); !errors.Is(err, workspacefs.ErrInvalid) {
		t.Fatal("plain Control accepted a credential", err)
	}
}

func TestQuotaWireMatchesSharedFixtures(t *testing.T) {
	signer, key := newKey(t)
	helper := startHelper(t, signer.PublicKey(), func(request []byte) []byte {
		if bytes.Contains(request, []byte(`"check"`)) {
			return append(fixture(t, "check-response.json"), '\n')
		}
		return append(fixture(t, "ensure-response.json"), '\n')
	})
	f := quotaControl(t, quotaConfiguration(t, helper.address, helper.hostKey), credentialInput(key))
	if err := f.quota.check(t.Context(), f.parameters.NamespaceID); err != nil {
		t.Fatal(err)
	}
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); err != nil {
		t.Fatal(err)
	}
	got := helper.received()
	if len(got) != 2 || !bytes.Equal(got[0], fixture(t, "check-request.json")) || !bytes.Equal(got[1], fixture(t, "ensure-request.json")) {
		t.Fatalf("requests differ from shared fixtures: %q", got)
	}
	// A receipt for a different bound is never accepted as enforcement.
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 4096); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatal(err)
	}
}

func TestQuotaErrorsMatchSharedFixture(t *testing.T) {
	var codes map[string]string
	if err := json.Unmarshal(fixture(t, "errors.json"), &codes); err != nil {
		t.Fatal(err)
	}
	classes := map[string]error{"invalid": workspacefs.ErrInvalid, "not_found": workspacefs.ErrNotFound, "ownership": workspacefs.ErrOwnership, "unavailable": workspacefs.ErrUnavailable}
	if len(codes) != len(quotaErrors) {
		t.Fatalf("helper error codes %d, adapter mapping %d", len(codes), len(quotaErrors))
	}
	for code, class := range codes {
		if quotaErrors[code] != classes[class] || classes[class] == nil {
			t.Fatalf("%s maps to %v, fixture says %s", code, quotaErrors[code], class)
		}
	}
	signer, key := newKey(t)
	response := `{"error": "object_deleted"}`
	helper := startHelper(t, signer.PublicKey(), func([]byte) []byte { return []byte(response + "\n") })
	f := quotaControl(t, quotaConfiguration(t, helper.address, helper.hostKey), credentialInput(key))
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrNotFound) {
		t.Fatal(err)
	}
	response = `{"error": "not_a_code"}`
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal(err)
	}
}

func TestQuotaHostKeyIsPinned(t *testing.T) {
	signer, key := newKey(t)
	helper := startHelper(t, signer.PublicKey(), func([]byte) []byte { return []byte("{}\n") })
	impostor, _ := newKey(t)
	f := quotaControl(t, quotaConfiguration(t, helper.address, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(impostor.PublicKey())))), credentialInput(key))
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrUnavailable) || errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal("unpinned host accepted", err)
	}
	if len(helper.received()) != 0 {
		t.Fatal("request sent to an unverified host")
	}
	_, other := newKey(t)
	f = quotaControl(t, quotaConfiguration(t, helper.address, helper.hostKey), credentialInput(other))
	if err := f.quota.check(t.Context(), f.parameters.NamespaceID); !errors.Is(err, workspacefs.ErrUnavailable) {
		t.Fatal("unauthorized client key accepted", err)
	}
}

func TestQuotaUnknownOutcomes(t *testing.T) {
	signer, key := newKey(t)
	var output []byte
	helper := startHelper(t, signer.PublicKey(), func([]byte) []byte { return output })
	f := quotaControl(t, quotaConfiguration(t, helper.address, helper.hostKey), credentialInput(key))
	for name, value := range map[string][]byte{"malformed": []byte("Traceback\n"), "empty": {}, "unknown field": []byte(`{"filesystem_uuid":"66666666-6666-4666-8666-666666666666","extra":1}`)} {
		output = value
		if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrUnconfirmed) {
			t.Fatal(name, "ensure", err)
		}
		if err := f.quota.check(t.Context(), f.parameters.NamespaceID); !errors.Is(err, workspacefs.ErrUnavailable) || errors.Is(err, workspacefs.ErrUnconfirmed) {
			t.Fatal(name, "check", err)
		}
	}
	output = nil
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := f.quota.ensure(ctx, fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal("timed out mutation reported settled", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	f = quotaControl(t, quotaConfiguration(t, closed, helper.hostKey), credentialInput(key))
	if err := f.quota.ensure(t.Context(), fixtureReference, f.configuration.ID, f.parameters.NamespaceID, 2048); !errors.Is(err, workspacefs.ErrUnavailable) || errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal("unreachable server reported unconfirmed", err)
	}
}
