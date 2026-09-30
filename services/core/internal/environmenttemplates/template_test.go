package environmenttemplates

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
)

func TestValidateName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *string
		valid bool
	}{
		{"absent", nil, true},
		{"one character", ptr("a"), true},
		{"longest", ptr(strings.Repeat("界", MaxNameLength)), true},
		{"empty", ptr(""), false},
		{"too long", ptr(strings.Repeat("a", MaxNameLength+1)), false},
		{"invalid UTF-8", ptr("\xff"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateName(tc.value); (err == nil) != tc.valid || (err != nil && !errors.Is(err, ErrInvalidInput)) {
				t.Fatalf("ValidateName() = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestInputValidate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input Input
		valid bool
	}{
		{"empty", Input{}, true},
		{"network unset ignores policy fields", Input{NetworkAccess: "bogus"}, true},
		{"restricted with domains", Input{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: []string{"example.com"}}, true},
		{"restricted without domains", Input{SetNetwork: true, NetworkAccess: "restricted"}, false},
		{"unknown access", Input{SetNetwork: true, NetworkAccess: "bogus"}, false},
		{"invalid name", Input{Name: ptr("")}, false},
		{"reserved env", Input{Setup: environmentconfig.Setup{Env: map[string]string{"PATH": "/bin"}}}, false},
		{"empty command", Input{Setup: environmentconfig.Setup{Commands: []environmentconfig.SetupCommand{{}}}}, false},
		{"file outside workspace", Input{Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/etc/passwd"}}}, false},
		{"inline file", Input{Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a.txt", Data: []byte("a")}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.input.Validate(); (err == nil) != tc.valid || (err != nil && !errors.Is(err, ErrInvalidInput)) {
				t.Fatalf("Validate() = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestWithDefaultNetwork(t *testing.T) {
	got := Input{AllowedDomains: []string{"example.com"}}.withDefaultNetwork()
	if !got.SetNetwork || got.NetworkAccess != "enabled" || got.AllowedDomains != nil {
		t.Fatalf("default network = %+v", got)
	}
	explicit := Input{SetNetwork: true, NetworkAccess: "disabled"}.withDefaultNetwork()
	if explicit.NetworkAccess != "disabled" {
		t.Fatalf("explicit network replaced: %+v", explicit)
	}
}

func TestListQueryValidate(t *testing.T) {
	for limit, valid := range map[int]bool{0: false, 1: true, MaxListLimit: true, MaxListLimit + 1: false, -1: false} {
		if err := (ListQuery{Limit: limit}).Validate(); (err == nil) != valid || (err != nil && !errors.Is(err, ErrInvalidInput)) {
			t.Fatalf("limit %d: Validate() = %v, want valid %v", limit, err, valid)
		}
	}
}

func ptr(value string) *string { return &value }
