package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	input.SpecificationDigest, input.DeploymentGeneration = stored.Identity.SpecificationDigest, stored.Identity.DeploymentGeneration
	input.NodeID, input.Credential = stored.Identity.NodeID, stored.Credential
	input.Provider, input.BackendFingerprint = stored.Identity.Provider, stored.Identity.BackendFingerprint
	input.CoreURL = coreURL
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// This also recovers a consumed registration whose success response was lost.
	target, err := endpoint(coreURL, "/api/v1/sandbox-node/identity")
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
			return retainEnrollment(dir, stored, out)
		}
	}
	target, err = endpoint(coreURL, "/api/v1/sandbox-node/enroll")
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
	return retainEnrollment(dir, stored, out)
}
func readEnrollment(r *http.Response) (EnrollmentResponse, error) {
	defer r.Body.Close()
	var out EnrollmentResponse
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return out, enrollmentRejection(r)
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return out, errors.New("invalid enrollment response")
	}
	return out, nil
}
func verifyEnrollment(s StoredIdentity, out EnrollmentResponse) (StoredIdentity, error) {
	if out.SpecificationDigest != s.Identity.SpecificationDigest || out.DeploymentGeneration != s.Identity.DeploymentGeneration || out.NodeID != s.Identity.NodeID || out.InstallationID != s.Identity.InstallationID || out.Provider != s.Identity.Provider {
		return StoredIdentity{}, errors.New("enrolled node identity mismatch")
	}
	if out.MaxActive < 1 || out.MaxRetained < out.MaxActive || out.MaxRetained > 1000000 {
		return StoredIdentity{}, errors.New("invalid approved node capacity")
	}
	s.Identity.MaxActive, s.Identity.MaxRetained = out.MaxActive, out.MaxRetained
	return s, nil
}

func retainEnrollment(dir string, stored StoredIdentity, out EnrollmentResponse) (StoredIdentity, error) {
	verified, err := verifyEnrollment(stored, out)
	if err != nil {
		return StoredIdentity{}, err
	}
	if err = writeIdentity(dir, verified); err != nil {
		return StoredIdentity{}, err
	}
	return verified, nil
}

// RefreshIdentity reads approved capacity using the retained credential before
// reconnecting. Core remains authoritative after an administrator changes it.
func RefreshIdentity(ctx context.Context, dir string) (StoredIdentity, error) {
	release, err := lockDirectory(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	defer release()
	stored, err := readIdentity(dir)
	if err != nil {
		return StoredIdentity{}, err
	}
	target, err := endpoint(stored.CoreURL, "/api/v1/sandbox-node/identity")
	if err != nil {
		return StoredIdentity{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"?node_id="+url.QueryEscape(stored.Identity.NodeID), nil)
	if err != nil {
		return StoredIdentity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+stored.Credential)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return StoredIdentity{}, errors.New("cannot confirm approved node capacity")
	}
	out, err := readEnrollment(response)
	if err != nil {
		return StoredIdentity{}, err
	}
	return retainEnrollment(dir, stored, out)
}

// coreErrorCode is the shape of Core's fixed error codes, the only part of a
// rejection body that reaches the node's output.
var coreErrorCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

// enrollmentRejection names Core's status and error code. Only Core's 401 is
// ErrAuthentication: the node was removed or retired, or the enrollment token is
// invalid, so retrying cannot succeed. A 403 may come from a proxy or firewall in
// front of Core, so it stays retryable.
func enrollmentRejection(r *http.Response) error {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&body)
	detail := "HTTP " + strconv.Itoa(r.StatusCode)
	if coreErrorCode.MatchString(body.Error.Code) {
		detail += " " + body.Error.Code
	}
	if r.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("node enrollment rejected (%s): %w", detail, ErrAuthentication)
	}
	return fmt.Errorf("node enrollment rejected (%s)", detail)
}
