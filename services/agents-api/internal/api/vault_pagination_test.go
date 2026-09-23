package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVaultStatusErrorEnvelopes(t *testing.T) {
	for _, path := range []string{"/v1/vaults", "/v1/vaults/vault-example/credentials"} {
		for _, query := range []string{"status=invalid", "status=", "status[]=active&status[]=invalid", "status[]="} {
			t.Run(path+"?"+query, func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
				if _, _, ok := readVaultPage(w, r); ok {
					t.Fatal("invalid status accepted")
				}
				assertListQueryError(t, w, "invalid_request_error", nil, "Failed to deserialize query string: status: data did not match any variant of untagged enum VaultStatusFilterParam")
			})
		}
	}
}

func TestVaultStatusGrammarUnchanged(t *testing.T) {
	for _, query := range []string{"status=active&status=archived", "status=active&status[]=archived"} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/vaults?"+query, nil)
			if _, _, ok := readVaultPage(w, r); ok {
				t.Fatal("invalid status grammar accepted")
			}
			assertListQueryError(t, w, "invalid_request", nil, "Supply status once or use status[] for an array.")
		})
	}
}
