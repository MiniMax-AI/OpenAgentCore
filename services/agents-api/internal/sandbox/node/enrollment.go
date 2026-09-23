package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Enroll consumes a short-lived enrollment token. InitIdentity must have been
// called with the local provider identity before invoking this function.
func Enroll(ctx context.Context, coreURL, dir, token string, input EnrollmentRequest) (StoredIdentity, error) {
	release, err := lockDirectory(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	defer release()
	stored, err := readIdentity(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	if stored.CoreURL != coreURL {
		return StoredIdentity{}, errors.New("Core URL differs from retained identity")
	}
	input.NodeID, input.Credential = stored.Identity.NodeID, stored.Credential
	input.Provider, input.BackendFingerprint = stored.Identity.Provider, stored.Identity.BackendFingerprint
	input.MaxActive, input.MaxRetained = stored.Identity.MaxActive, stored.Identity.MaxRetained
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// This also recovers a consumed registration whose success response was lost.
	target, err := endpoint(coreURL, "/core/v1/sandbox/node/identity")
	if err != nil {
		return StoredIdentity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"?node_id="+url.QueryEscape(input.NodeID), nil)
	if err != nil {
		return StoredIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+stored.Credential)
	if response, err := client.Do(req); err == nil {
		out, readErr := readEnrollment(response)
		if readErr == nil {
			return verifyEnrollment(stored, out)
		}
	}
	target, err = endpoint(coreURL, "/core/v1/sandbox/enroll")
	if err != nil {
		return StoredIdentity{}, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return StoredIdentity{}, err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return StoredIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return StoredIdentity{}, errors.New("enrollment response unconfirmed; retry with the same state directory")
	}
	out, err := readEnrollment(response)
	if err != nil {
		return StoredIdentity{}, err
	}
	return verifyEnrollment(stored, out)
}
func readEnrollment(r *http.Response) (EnrollmentResponse, error) {
	defer r.Body.Close()
	var out EnrollmentResponse
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return out, errors.New("node enrollment rejected")
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return out, errors.New("invalid enrollment response")
	}
	return out, nil
}
func verifyEnrollment(s StoredIdentity, out EnrollmentResponse) (StoredIdentity, error) {
	if out.NodeID != s.Identity.NodeID || out.InstallationID != s.Identity.InstallationID || out.Provider != s.Identity.Provider {
		return StoredIdentity{}, errors.New("enrolled node identity mismatch")
	}
	return s, nil
}
