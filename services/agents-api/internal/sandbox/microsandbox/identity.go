package microsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func validID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}
func ValidReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func (c Config) Validate() error {
	if !validID(c.InstallationID) || !filepath.IsAbs(c.HelperPath) || !filepath.IsAbs(c.RuntimeHome) || !filepath.IsAbs(c.RuntimePath) || !filepath.IsAbs(c.FirmwarePath) || !validHash(c.RuntimeSHA256) || !validHash(c.FirmwareSHA256) || c.MemoryMiB == 0 || c.CPUs == 0 || c.RootDiskMiB == 0 || c.EnvironmentDiskMiB == 0 {
		return sandbox.ErrInvalid
	}
	imageName, digest, pinned := strings.Cut(c.Image, "@sha256:")
	if !pinned || imageName == "" || strings.Contains(imageName, "@") || !validHash(digest) {
		return sandbox.ErrInvalid
	}
	if c.Network.DefaultEgress != "allow" && c.Network.DefaultEgress != "deny" {
		return sandbox.ErrInvalid
	}
	if c.Network.DefaultIngress != "allow" && c.Network.DefaultIngress != "deny" {
		return sandbox.ErrInvalid
	}
	for _, r := range c.Network.Rules {
		if (r.Action != "allow" && r.Action != "deny") || (r.Direction != "egress" && r.Direction != "ingress") || r.Destination == "" {
			return sandbox.ErrInvalid
		}
	}
	return nil
}
func Name(c Config, r sandbox.Reference, g uint64) string {
	h := sha256.Sum256([]byte(c.InstallationID + ":" + r.TenantID + ":" + r.EnvironmentID + ":" + r.AllocationID))
	return fmt.Sprintf("parsar-%x-g%d", h[:16], g)
}
func SnapshotReference(c Config, r sandbox.Reference, operation string) string {
	return Name(c, r, 0) + ":s-" + operation
}
func Labels(c Config, r sandbox.Reference) map[string]string {
	return map[string]string{"io.parsar.installation": c.InstallationID, "io.parsar.tenant": r.TenantID, "io.parsar.environment": r.EnvironmentID, "io.parsar.allocation": r.AllocationID}
}
func ValidateCompute(c Config, r sandbox.Reference, v Compute) error {
	if !ValidReference(r) || v.Name != Name(c, r, v.Generation) {
		return sandbox.ErrInvalid
	}
	if v.Generation == 0 {
		if v.RestoredFrom != nil {
			return sandbox.ErrInvalid
		}
	} else {
		if v.RestoredFrom == nil || ValidateSnapshot(c, r, *v.RestoredFrom) != nil || v.RestoredFrom.SourceGeneration >= v.Generation {
			return sandbox.ErrInvalid
		}
	}
	return nil
}
func ValidateSnapshot(c Config, r sandbox.Reference, s SnapshotIdentity) error {
	if !ValidReference(r) || !validID(s.OperationID) || s.Reference != SnapshotReference(c, r, s.OperationID) || s.SourceName != Name(c, r, s.SourceGeneration) || s.SourceID == "" || s.ID == "" || s.Digest == "" || s.CheckpointID == "" || s.CheckpointRoot == "" {
		return sandbox.ErrInvalid
	}
	return nil
}
func ValidateBootstrap(b sandbox.Bootstrap) error {
	u, e := url.Parse(b.CoreURL)
	if !ValidReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(b.Credential) == "" {
		return sandbox.ErrInvalid
	}
	return (agentnetwork.Policy{Access: b.NetworkAccess, AllowedDomains: b.AllowedDomains}).Validate()
}
func ValidateCommand(c sandbox.Command) error {
	if len(c.Args) == 0 || c.Args[0] == "" || len(c.Stdin) > sandbox.MaxCommandInputBytes || (c.Directory != "" && !filepath.IsAbs(c.Directory)) {
		return sandbox.ErrInvalid
	}
	for _, a := range c.Args {
		if strings.IndexByte(a, 0) >= 0 {
			return sandbox.ErrInvalid
		}
	}
	return nil
}
func ValidateRequest(q Request) error {
	if q.Version != ProtocolVersion || q.Config.Validate() != nil || !ValidReference(q.Reference) || q.Deadline.IsZero() {
		return sandbox.ErrInvalid
	}
	switch q.Operation {
	case "create":
		if q.Bootstrap == nil || q.Bootstrap.Reference != q.Reference || ValidateBootstrap(*q.Bootstrap) != nil {
			return sandbox.ErrInvalid
		}
	case "inspect", "kill", "resume_compute":
		if q.Operation == "resume_compute" && q.Compute.ID == "" {
			return sandbox.ErrInvalid
		}
		return ValidateCompute(q.Config, q.Reference, q.Compute)
	case "command":
		if q.Command == nil || ValidateCommand(*q.Command) != nil {
			return sandbox.ErrInvalid
		}
		return ValidateCompute(q.Config, q.Reference, q.Compute)
	case "suspend":
		s := q.Suspend
		if s == nil || s.Reference != q.Reference || !validID(s.OperationID) || s.Source.ID == "" || ValidateCompute(q.Config, q.Reference, s.Source) != nil {
			return sandbox.ErrInvalid
		}
		if s.Snapshot != nil && (ValidateSnapshot(q.Config, q.Reference, *s.Snapshot) != nil || s.Snapshot.OperationID != s.OperationID || s.Snapshot.SourceID != s.Source.ID || s.Snapshot.SourceName != s.Source.Name || s.Snapshot.SourceGeneration != s.Source.Generation) {
			return sandbox.ErrInvalid
		}
	case "resume":
		v := q.Resume
		if v == nil || v.Reference != q.Reference || !validID(v.OperationID) || ValidateSnapshot(q.Config, q.Reference, v.Snapshot) != nil || ValidateCompute(q.Config, q.Reference, v.Target) != nil || v.Target.RestoredFrom == nil || *v.Target.RestoredFrom != v.Snapshot {
			return sandbox.ErrInvalid
		}
	case "delete_snapshot":
		if q.Snapshot == nil {
			return sandbox.ErrInvalid
		}
		return ValidateSnapshot(q.Config, q.Reference, *q.Snapshot)
	default:
		return sandbox.ErrInvalid
	}
	return nil
}
