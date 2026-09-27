package main

import "testing"

func TestOAuthRefreshOperatorPolicy(t *testing.T) {
	for _, raw := range []string{"", "https://issuer.example", "https://issuer.example, https://10.0.0.1:9443"} {
		t.Setenv("OAC_OAUTH_TRUSTED_ORIGINS", raw)
		if _, err := oauthRefreshClient(); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"http://issuer.example", "https://issuer.example/token", "https://issuer.example,", "https://user:secret@issuer.example"} {
		t.Setenv("OAC_OAUTH_TRUSTED_ORIGINS", raw)
		if _, err := oauthRefreshClient(); err == nil {
			t.Fatal("invalid issuer policy accepted")
		}
	}
}
