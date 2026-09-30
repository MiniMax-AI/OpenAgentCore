package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

	"github.com/google/uuid"
)

// Node capacity and name limits.
const (
	maxNodeName     = 128
	maxNodeCapacity = 1000000
	// maxReportedGenerations bounds the generations one node report names.
	maxReportedGenerations = 8
)

// parseID returns the canonical form of a nonzero UUID, and ErrInvalidInput
// for anything else.
func parseID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidInput
	}
	return id.String(), nil
}

// tokenDigest is the stored form of a node credential or enrollment token.
func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// validDigest accepts a lowercase hexadecimal SHA-256 digest.
func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

// validGeneration accepts a generation a database row can hold.
func validGeneration(generation uint64) bool {
	return generation != 0 && generation <= math.MaxInt64
}

// validateNode checks a node name and capacity. max_retained is at least
// max_active.
func validateNode(name string, active, retained int) error {
	if strings.TrimSpace(name) == "" || len(name) > maxNodeName || strings.ContainsAny(name, "\x00\r\n") {
		return &NodeValidationError{Code: "invalid_name", Param: "name", MaxLength: maxNodeName}
	}
	if active < 1 || active > maxNodeCapacity {
		return &NodeValidationError{Code: "invalid_node_capacity", Param: "max_active"}
	}
	if retained < active || retained > maxNodeCapacity {
		return &NodeValidationError{Code: "invalid_node_capacity", Param: "max_retained"}
	}
	return nil
}

// validNodeCredential accepts a node credential as enrollment stores it.
func validNodeCredential(credential string) bool {
	return len(credential) >= 32 && len(credential) <= 256 && !strings.ContainsAny(credential, " \t\r\n")
}

// checkGeneration rejects a Web setup change whose expected generation or
// installation is not the deployment's.
func checkGeneration(d Record, installation string, generation uint64) error {
	if d.Generation != generation {
		return &GenerationStaleError{CurrentGeneration: d.Generation}
	}
	if !d.WebManaged || d.InstallationID != installation {
		return ErrConflict
	}
	return nil
}

// initialized reports whether an installation and a provider are selected.
func initialized(d Record) bool {
	return d.InstallationID != "" && d.Provider != ""
}
