package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestEnvironmentInstallationClaimLifetimeAndRetries(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	ctx := t.Context()
	p := createTestProject(t, pool).Principal
	input := environmentInput(uuid.NewString(), "self_hosted", "/workspace")
	input.Creator = p.Subject()
	session, err := s.CreateSession(ctx, p.TenantID, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, p.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	token, expires, err := s.AuthorizeEnvironmentInstallation(ctx, p, environment.ID, "build")
	if err != nil || expires <= time.Now().Unix() || expires > time.Now().Add(31*time.Minute).Unix() {
		t.Fatal("authorization", err)
	}
	for _, pair := range [][2]string{{token + "x", "build"}, {token, "other-build"}, {"", "build"}} {
		if _, err := s.ValidateEnvironmentInstallation(ctx, pair[0], pair[1]); !errors.Is(err, sessions.ErrInstallationAuthorization) {
			t.Fatal("accepted invalid authorization", err)
		}
	}
	payload, _, _ := strings.Cut(token, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	var expired sessions.InstallationAuthorization
	_ = json.Unmarshal(raw, &expired)
	expired.ExpiresAt = time.Now().Add(-time.Second).Unix()
	raw, _ = json.Marshal(expired)
	payload = base64.RawURLEncoding.EncodeToString(raw)
	signature, _ := cipher.Fingerprint("environment-installation", payload)
	if _, err := s.ValidateEnvironmentInstallation(ctx, payload+"."+signature, "build"); !errors.Is(err, sessions.ErrInstallationAuthorization) {
		t.Fatal("accepted expired grant", err)
	}
	one, _, _ := newExecutorSecret()
	two, _, _ := newExecutorSecret()
	secrets := []string{one, two}
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range secrets {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = s.ClaimEnvironmentInstallation(ctx, token, "build", secrets[i]) }()
	}
	wg.Wait()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner >= 0 {
				t.Fatal("two machines claimed one Environment")
			}
			winner = i
		} else if !errors.Is(err, sessions.ErrExecutorCredentialExists) {
			t.Fatal(err)
		}
	}
	if winner < 0 {
		t.Fatal("no claim succeeded")
	}
	if err := s.ClaimEnvironmentInstallation(ctx, token, "build", secrets[winner]); err != nil {
		t.Fatal("lost-response retry", err)
	}
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, environment.ID, executorDigest(secrets[winner])); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeExecutorCredential(ctx, p, environment.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimEnvironmentInstallation(ctx, token, "build", secrets[winner]); !errors.Is(err, sessions.ErrExecutorCredentialExists) {
		t.Fatal("revoked key resurrected", err)
	}
	if err := s.DeleteSession(ctx, p.TenantID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateEnvironmentInstallation(ctx, token, "build"); !errors.Is(err, sessions.ErrInstallationAuthorization) {
		t.Fatal("deleted Session grant accepted", err)
	}
}
