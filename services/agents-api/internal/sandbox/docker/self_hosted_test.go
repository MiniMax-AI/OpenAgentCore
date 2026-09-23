package docker

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func selfHostedFixture() SelfHostedLaunch {
	environment := uuid.NewString()
	return SelfHostedLaunch{InstallationID: uuid.NewString(), EnvironmentID: environment, RemoteURL: "wss://core.example/api/v1/agent-daemon/ws", Image: "sha256:" + strings.Repeat("a", 64), Seccomp: `{}`, Credential: ExecutorCredential{KeyID: uuid.NewString(), EnvironmentID: environment, Token: "synthetic-private-token"}}
}

func TestSelfHostedRejectsUnsafeLaunchBeforeDocker(t *testing.T) {
	for _, change := range []func(*SelfHostedLaunch){
		func(v *SelfHostedLaunch) { v.RemoteURL = "ws://localhost/api/v1/agent-daemon/ws" },
		func(v *SelfHostedLaunch) { v.RemoteURL += "?secret=value" },
		func(v *SelfHostedLaunch) { v.RemoteURL = "wss://user:secret@core.example/api/v1/agent-daemon/ws" },
		func(v *SelfHostedLaunch) { v.Credential.EnvironmentID = "" },
		func(v *SelfHostedLaunch) { v.Credential.EnvironmentID = uuid.NewString() },
		func(v *SelfHostedLaunch) { v.Image = "runtime:latest" },
	} {
		value := selfHostedFixture()
		change(&value)
		if _, err := LaunchSelfHosted(t.Context(), nil, value); err == nil {
			t.Fatal("unsafe launch accepted")
		}
	}
}

func TestSelfHostedLaunchUsesQualifiedIsolationAndPrivateBootstrap(t *testing.T) {
	value := selfHostedFixture()
	var configuration struct {
		container.Config
		HostConfig container.HostConfig
	}
	privateFiles := map[string]*tar.Header{}
	contents := map[string]string{}
	started, creates := false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.52")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && (strings.HasPrefix(path, "/volumes/") || strings.HasSuffix(path, "/json")):
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"message":"not found"}`)
		case path == "/volumes/create":
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			_ = json.NewEncoder(w).Encode(request)
		case path == "/containers/create":
			creates++
			if err := json.NewDecoder(r.Body).Decode(&configuration); err != nil {
				t.Error(err)
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"Id":"runtime-id","Warnings":[]}`)
		case path == "/containers/runtime-id/archive":
			reader := tar.NewReader(r.Body)
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					break
				}
				name := r.URL.Query().Get("path") + "/" + header.Name
				privateFiles[name] = header
				content, _ := io.ReadAll(reader)
				contents[name] = string(content)
			}
			w.WriteHeader(200)
		case path == "/containers/runtime-id/start":
			started = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	name, err := LaunchSelfHosted(t.Context(), c, value)
	if err != nil || name != value.Name() || creates != 1 || !started {
		t.Fatal("launch", err)
	}
	qualified := runtimeContainerOptions(Config{Image: value.Image, Seccomp: value.Seccomp, Network: "bridge", NestedSandbox: true}, name, configuration.Labels, nil)
	qualified.HostConfig.RestartPolicy = configuration.HostConfig.RestartPolicy
	if !reflect.DeepEqual(*qualified.HostConfig, configuration.HostConfig) {
		t.Fatal("user Runtime changed qualified isolation")
	}
	if configuration.User != "1000:1000" || configuration.WorkingDir != "/environment/workspace" || len(configuration.Env) != 0 {
		t.Fatal("unsafe process configuration")
	}
	command, _ := json.Marshal(configuration.Cmd)
	if strings.Contains(string(command), value.Credential.Token) || !strings.Contains(string(command), value.RemoteURL) {
		t.Fatal("credential in argv or remote URL changed")
	}
	key := "/home/runtime/.parsar/parsar-daemon/executor-key.json"
	for name, header := range privateFiles {
		if header.Uid != 1000 || header.Gid != 1000 || header.Mode != 0700 && header.Mode != 0600 {
			t.Fatal("unsafe archive ownership", name)
		}
	}
	if privateFiles[key] == nil || privateFiles[key].Mode != 0600 || !strings.Contains(contents[key], value.Credential.Token) || privateFiles["/environment/workspace"] == nil {
		t.Fatal("missing protected credential or workspace")
	}
}

func TestSelfHostedRetainsExistingRuntimeWithoutWrites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/json") {
			t.Fatal("unexpected mutation")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Id":"existing"}`)
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := LaunchSelfHosted(t.Context(), c, selfHostedFixture()); !errors.Is(err, sandbox.ErrExists) {
		t.Fatal("existing history adopted", err)
	}
}
