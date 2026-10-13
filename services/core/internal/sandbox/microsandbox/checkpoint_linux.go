//go:build linux

package microsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// CheckpointStore opens only an explicitly configured, private archive root.
// Its marker is an operator-owned identity, never inferred from workspace paths.
func CheckpointStore(c Config) (string, error) {
	if !filepath.IsAbs(c.CheckpointRoot) || filepath.Clean(c.CheckpointRoot) != c.CheckpointRoot {
		return "", sandbox.ErrInvalid
	}
	real, err := filepath.EvalSymlinks(c.CheckpointRoot)
	if err != nil {
		return "", err
	}
	if real != c.CheckpointRoot {
		return "", sandbox.ErrOwnership
	}
	fd, err := syscall.Open(c.CheckpointRoot, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), c.CheckpointRoot)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return "", sandbox.ErrOwnership
	}
	marker, err := syscall.Openat(fd, ".oac-checkpoint-store", syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	m := os.NewFile(uintptr(marker), "checkpoint marker")
	defer m.Close()
	st, err = m.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return "", sandbox.ErrOwnership
	}
	data, err := io.ReadAll(io.LimitReader(m, 38))
	if err != nil {
		return "", err
	}
	id := strings.TrimSuffix(string(data), "\n")
	if !validID(id) || string(data) != id+"\n" {
		return "", sandbox.ErrOwnership
	}
	h := sha256.Sum256([]byte(c.InstallationID + ":" + id))
	return hex.EncodeToString(h[:]), nil
}

// CheckpointClass deliberately admits only identical native and host execution
// profiles. Paths, hostname and local SDK cache identities are not compatibility.
func CheckpointClass(c Config) (sandbox.CheckpointCompatibility, error) {
	domain, err := CheckpointStore(c)
	if err != nil {
		return sandbox.CheckpointCompatibility{}, err
	}
	cpu, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return sandbox.CheckpointCompatibility{}, err
	}
	stable := checkpointCPUProfiles(string(cpu))
	if len(stable) == 0 {
		return sandbox.CheckpointCompatibility{}, sandbox.ErrInvalid
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return sandbox.CheckpointCompatibility{}, err
	}
	raw, _ := json.Marshal(struct {
		SDK, Runtime, Firmware, Image, Kernel string
		CPUs                                  uint8
		Memory, Root, Environment             uint32
		CPU                                   []string
	}{SDKVersion, c.RuntimeSHA256, c.FirmwareSHA256, c.Image, strings.TrimSpace(string(kernel)), c.CPUs, c.MemoryMiB, c.RootDiskMiB, c.EnvironmentDiskMiB, stable})
	h := sha256.Sum256(raw)
	return sandbox.CheckpointCompatibility{ArtifactDomain: domain, ExecutionClass: hex.EncodeToString(h[:])}, nil
}

// Preserve each processor's feature/model association. Multiplicity and logical
// processor indexes are irrelevant; distinct heterogeneous profiles are not.
func checkpointCPUProfiles(cpuinfo string) []string {
	profiles := map[string]bool{}
	for _, block := range strings.Split(strings.TrimSpace(cpuinfo), "\n\n") {
		fields := []string{}
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			switch key {
			case "vendor_id", "cpu family", "model", "stepping", "flags", "Features", "CPU implementer", "CPU architecture", "CPU part", "CPU revision":
				value = strings.TrimSpace(value)
				if key == "flags" || key == "Features" {
					features := strings.Fields(value)
					sort.Strings(features)
					value = strings.Join(features, " ")
				}
				fields = append(fields, key+":"+value)
			}
		}
		if len(fields) > 0 {
			sort.Strings(fields)
			profiles[strings.Join(fields, "\n")] = true
		}
	}
	result := make([]string, 0, len(profiles))
	for profile := range profiles {
		result = append(result, profile)
	}
	sort.Strings(result)
	return result
}
