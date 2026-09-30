package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func environmentRuntimeFixture(t *testing.T) (root, workspace, credential string, bound environmentEnrollment) {
	t.Helper()
	base := t.TempDir()
	root, workspace = filepath.Join(base, "private"), filepath.Join(base, "workspace")
	for _, dir := range []string{filepath.Join(root, "daemon"), workspace} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	credential = filepath.Join(root, "daemon", "executor.json")
	if err := os.WriteFile(credential, []byte("private-test-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	t.Setenv("OAC_RUNTIME_WORKSPACE", workspace)
	for _, name := range []string{"OAC_RUNTIME_ENVIRONMENT_ID", "OAC_RUNTIME_SESSION_ID", "OAC_RUNTIME_NETWORK_ACCESS", "OAC_RUNTIME_ALLOWED_DOMAINS"} {
		t.Setenv(name, "")
	}
	return root, workspace, credential, environmentEnrollment{uuid.NewString(), uuid.NewString(), uuid.NewString(), "/workspace"}
}

func TestBindEnvironmentRuntimeUsesCanonicalOperatorWorkspace(t *testing.T) {
	for _, physical := range []bool{false, true} {
		t.Run(map[bool]string{false: "logical", true: "physical"}[physical], func(t *testing.T) {
			root, workspace, credential, bound := environmentRuntimeFixture(t)
			if physical {
				bound.WorkspaceDirectory = workspace
			}
			if err := bindEnvironmentRuntime("wss://core/api/v1/agent-daemon/ws", bound, credential); err != nil {
				t.Fatal(err)
			}
			raw, err := readEnvironmentPrivateFile(filepath.Join(root, "daemon", "environment.json"))
			if err != nil {
				t.Fatal(err)
			}
			var receipt environmentBinding
			if decodeEnvironmentJSON(raw, &receipt) != nil || receipt.LocalWorkspace != workspace || receipt.Enrollment != bound {
				t.Fatal("workspace identity changed", receipt)
			}
			if err := bindEnvironmentRuntime(receipt.RemoteURL, bound, credential); err != nil {
				t.Fatal("same binding refused", err)
			}
			replacement := filepath.Join(filepath.Dir(workspace), "replacement")
			if err := os.Mkdir(replacement, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OAC_RUNTIME_WORKSPACE", replacement)
			if err := bindEnvironmentRuntime(receipt.RemoteURL, bound, credential); err == nil {
				t.Fatal("existing workspace identity replaced")
			}
			after, _ := os.ReadFile(filepath.Join(root, "daemon", "environment.json"))
			if string(after) != string(raw) {
				t.Fatal("failed rebind changed identity")
			}
			contents, _ := os.ReadFile(credential)
			if string(contents) != "private-test-credential" {
				t.Fatal("credential changed")
			}
		})
	}
}

func TestBindEnvironmentRuntimeChecksIdentityAndPathValidity(t *testing.T) {
	for _, kind := range []string{"relative", "unclean", "missing", "file", "symlink", "parent symlink", "workspace contains private", "workspace inside private", "foreign enrollment", "credential outside private", "identity conflict"} {
		t.Run(kind, func(t *testing.T) {
			root, workspace, credential, bound := environmentRuntimeFixture(t)
			selected := workspace
			switch kind {
			case "relative":
				selected = "workspace"
			case "unclean":
				selected = workspace + "/../workspace"
			case "missing":
				selected = filepath.Join(filepath.Dir(workspace), "missing")
			case "file":
				selected = filepath.Join(filepath.Dir(workspace), "regular")
				if err := os.WriteFile(selected, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				selected = filepath.Join(filepath.Dir(workspace), "linked")
				if err := os.Symlink(workspace, selected); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				child := filepath.Join(workspace, "child")
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(filepath.Dir(workspace), "linked-parent")
				if err := os.Symlink(workspace, alias); err != nil {
					t.Fatal(err)
				}
				selected = filepath.Join(alias, "child")
			case "workspace contains private":
				selected = filepath.Dir(root)
			case "workspace inside private":
				selected = filepath.Join(root, "nested-workspace")
				if err := os.Mkdir(selected, 0700); err != nil {
					t.Fatal(err)
				}
			case "foreign enrollment":
				bound.WorkspaceDirectory = filepath.Join(filepath.Dir(workspace), "different")
			case "credential outside private":
				credential = filepath.Join(filepath.Dir(root), "credential")
				if err := os.WriteFile(credential, []byte("private-test-credential"), 0600); err != nil {
					t.Fatal(err)
				}
			case "identity conflict":
				t.Setenv("OAC_RUNTIME_ENVIRONMENT_ID", uuid.NewString())
			}
			t.Setenv("OAC_RUNTIME_WORKSPACE", selected)
			allowed := kind == "symlink" || kind == "parent symlink" || kind == "workspace contains private" || kind == "workspace inside private" || kind == "credential outside private"
			err := bindEnvironmentRuntime("wss://core/api/v1/agent-daemon/ws", bound, credential)
			if allowed {
				if err != nil {
					t.Fatal("operator layout rejected", err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid binding accepted")
				}
				if _, err := os.Stat(filepath.Join(root, "daemon", "environment.json")); !os.IsNotExist(err) {
					t.Fatal("invalid binding published identity", err)
				}
			}

		})
	}
}
