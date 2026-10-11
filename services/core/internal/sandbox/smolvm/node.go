package smolvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

// Native is the node-local provider configuration. ImageFile is the unpacked
// release OCI archive, not an image name resolved from a mutable registry tag.
type Native struct {
	Socket      string `json:"socket"`
	ImageFile   string `json:"image_file"`
	ReceiptRoot string `json:"receipt_root"`
}

func canonicalSocket(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "unix" && parsed.Host == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == "" &&
		filepath.IsAbs(parsed.Path) && parsed.Path != "/" && filepath.Clean(parsed.Path) == parsed.Path && value == "unix://"+parsed.Path
}

func privateSocket(address string) error {
	socket, err := os.Lstat(address[len("unix://"):])
	if err != nil {
		return fmt.Errorf("cannot inspect smolvm API socket: %w", err)
	}
	if socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm() != 0600 {
		return fmt.Errorf("%w: smolvm API socket must be private (mode 0600)", sandbox.ErrInvalid)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", sandbox.ErrInvalid
	}
	sha := sha256.New()
	if _, err := io.Copy(sha, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sha.Sum(nil)), nil
}

func BuildNode(config sandbox.NodeConfig, _ sandbox.LocalOptions, result *sandbox.Built) (func(), error) {
	closeProvider := func() {}
	var native Native
	if sandbox.DecodeConfigurationObject(config.Native, &native, "socket", "image_file", "receipt_root") != nil ||
		!canonicalSocket(native.Socket) || !filepath.IsAbs(native.ImageFile) || filepath.Clean(native.ImageFile) != native.ImageFile {
		return closeProvider, errors.New("invalid managed smolvm node configuration")
	}
	if config.Specification.Runtime == nil {
		return closeProvider, sandbox.ErrInvalid
	}
	if err := privateSocket(native.Socket); err != nil {
		return closeProvider, err
	}
	hash, err := hashFile(native.ImageFile)
	if err != nil {
		return closeProvider, fmt.Errorf("cannot verify smolvm Runtime OCI archive: %w", err)
	}
	if err := verifyOCIRelease(native.ImageFile, config.Specification.Runtime.ImageManifestDigest, config.Specification.Runtime.ImageID); err != nil {
		return closeProvider, fmt.Errorf("smolvm Runtime release mismatch: %w", err)
	}
	client := sdk.NewClient(native.Socket)
	provider, err := New(client, Config{InstallationID: config.InstallationID, ImagePath: native.ImageFile, ImageHash: hash, ReceiptRoot: native.ReceiptRoot, Resources: config.Specification.Resources})
	if err != nil {
		return closeProvider, err
	}
	result.Provider = provider
	result.BackendFingerprint = sandbox.BackendFingerprint(config.Provider, native.Socket)
	result.Probe = func(ctx context.Context) (*sandbox.CheckpointCompatibility, error) {
		if _, err := client.Health(ctx); err != nil {
			return nil, fmt.Errorf("%w: smolvm service unavailable: %v", sandbox.ErrProviderUnavailable, err)
		}
		return nil, nil
	}
	return closeProvider, nil
}
