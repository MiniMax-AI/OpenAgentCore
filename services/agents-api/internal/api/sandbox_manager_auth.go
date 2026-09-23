package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// DeploymentAuthenticator is deliberately separate from project API principals.
type DeploymentAuthenticator struct{ digests map[[32]byte]struct{} }

func NewDeploymentAuthenticator(digests []string) (*DeploymentAuthenticator, error) {
	if len(digests) == 0 {
		return nil, errors.New("at least one deployment administrator digest is required")
	}
	result := &DeploymentAuthenticator{digests: make(map[[32]byte]struct{}, len(digests))}
	for _, value := range digests {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return nil, errors.New("invalid deployment administrator digest")
		}
		digest := [32]byte(raw)
		if _, exists := result.digests[digest]; exists {
			return nil, errors.New("duplicate deployment administrator digest")
		}
		result.digests[digest] = struct{}{}
	}
	return result, nil
}
func sandboxBearer(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	returnValue := ""
	valid := len(r.Header.Values("Authorization")) == 1 && len(parts) == 2 && strings.EqualFold(parts[0], "Bearer")
	if valid {
		returnValue = parts[1]
	}
	return returnValue, valid
}
func (a *DeploymentAuthenticator) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, valid := sandboxBearer(r)
		_, exists := a.digests[sha256.Sum256([]byte(token))]
		if !valid || !exists {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid_admin_key", "A deployment administrator bearer key is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
