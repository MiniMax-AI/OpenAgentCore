package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVaultStatusErrorEnvelopes(t *testing.T) {
	for _, path := range []string{"/v1/vaults", "/v1/vaults/vault-example/credentials"} {
		for _, query := range []string{"status=invalid", "status=", "status[]=active&status[]=invalid", "status[]=", "status=active&status[]=invalid", "status=invalid&status[]=active"} {
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

func TestVaultStatusUnionAndDuplicates(t *testing.T) {
	for _, path := range []string{"/v1/vaults", "/v1/vaults/vault-example/credentials"} {
		for _, test := range []struct {
			query    string
			statuses []string
		}{
			{"status=active&status[]=archived", []string{"active", "archived"}},
			{"status[]=archived&status=active", []string{"active", "archived"}},
			{"status=active&status[]=active&status[]=archived", []string{"active", "active", "archived"}},
			{"status=archived&unknown=1", []string{"archived"}},
		} {
			t.Run(path+"?"+test.query, func(t *testing.T) {
				w := httptest.NewRecorder()
				_, statuses, ok := readVaultPage(w, httptest.NewRequest(http.MethodGet, path+"?"+test.query, nil))
				if !ok || !reflect.DeepEqual(statuses, test.statuses) || w.Body.Len() != 0 {
					t.Fatalf("statuses=%v ok=%t response=%s", statuses, ok, w.Body.String())
				}
			})
		}
		for _, key := range []string{"status", "limit", "after", "order"} {
			t.Run(path+"/duplicate-"+key, func(t *testing.T) {
				w := httptest.NewRecorder()
				values := map[string]string{"status": "active", "limit": "5", "after": "x", "order": "asc"}
				query := key + "=" + values[key] + "&" + key + "=" + values[key]
				if _, _, ok := readVaultPage(w, httptest.NewRequest(http.MethodGet, path+"?"+query, nil)); ok {
					t.Fatal("repeated key accepted")
				}
				assertListQueryError(t, w, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `"+key+"`")
			})
		}
	}
}

func TestVaultLimitClamp(t *testing.T) {
	for _, path := range []string{"/v1/vaults", "/v1/vaults/vault-example/credentials"} {
		for query, limit := range map[string]int{
			"": 20, "limit=0": 1, "limit=-1": 1, "limit=-8": 1, "limit=3": 3, "limit=100": 100, "limit=101": 100,
			"limit=9999999999999999999999999": 100, "limit=-9999999999999999999999999": 1,
		} {
			t.Run(path+"?"+query, func(t *testing.T) {
				w := httptest.NewRecorder()
				page, _, ok := readVaultPage(w, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
				if !ok || page.limit != limit || w.Body.Len() != 0 {
					t.Fatalf("page=%+v ok=%t response=%s", page, ok, w.Body.String())
				}
			})
		}
		for _, query := range []string{"limit=abc", "limit=1.5", "limit=", "limit=null"} {
			t.Run(path+"?"+query, func(t *testing.T) {
				w := httptest.NewRecorder()
				if _, _, ok := readVaultPage(w, httptest.NewRequest(http.MethodGet, path+"?"+query, nil)); ok {
					t.Fatal("non-integer limit accepted")
				}
				assertListQueryError(t, w, "invalid_request_error", nil, invalidDigit)
			})
		}
	}
}
