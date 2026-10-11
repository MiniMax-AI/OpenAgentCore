package smolvm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type receipt struct {
	Installation string            `json:"installation"`
	Reference    sandbox.Reference `json:"reference"`
	NativeID     string            `json:"native_id"`
	ImageRef     string            `json:"image_ref"`
}

func resourceName(installation string, ref sandbox.Reference) string {
	key := sha256.Sum256([]byte(installation + ":" + ref.TenantID + ":" + ref.EnvironmentID + ":" + ref.AllocationID))
	return "oac-smolvm-" + hex.EncodeToString(key[:16])
}
func (p *Provider) receiptPath(ref sandbox.Reference) string {
	return filepath.Join(p.config.ReceiptRoot, resourceName(p.config.InstallationID, ref)+".json")
}
func (p *Provider) readReceipt(ref sandbox.Reference) (*receipt, error) {
	raw, err := os.ReadFile(p.receiptPath(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value receipt
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: invalid bootstrap receipt", sandbox.ErrOwnership)
	}
	if value.Installation != p.config.InstallationID || value.Reference != ref || value.NativeID == "" || value.ImageRef != p.imageRef {
		return nil, sandbox.ErrOwnership
	}
	return &value, nil
}
func syncReceiptDir(root string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (p *Provider) writeReceipt(ref sandbox.Reference, nativeID string) error {
	if value, err := p.readReceipt(ref); err != nil {
		return err
	} else if value != nil {
		return sandbox.ErrExists
	}
	value := receipt{Installation: p.config.InstallationID, Reference: ref, NativeID: nativeID, ImageRef: p.imageRef}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := p.receiptPath(ref) + "." + hex.EncodeToString(suffix) + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Link publishes only if no receipt exists. Rename replaces another
	// writer's receipt on Unix and could silently authorize the wrong VM.
	if err := os.Link(tmp, p.receiptPath(ref)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return sandbox.ErrExists
		}
		return err
	}
	return syncReceiptDir(p.config.ReceiptRoot)
}
func (p *Provider) removeReceipt(ref sandbox.Reference) error {
	value, err := p.readReceipt(ref)
	if err != nil || value == nil {
		return err
	}
	if err := os.Remove(p.receiptPath(ref)); err != nil {
		return err
	}
	return syncReceiptDir(p.config.ReceiptRoot)
}
