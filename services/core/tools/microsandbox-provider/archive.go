//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// This adapter-private namespace is never a guest mount. The allocation lock
// serializes archive publication, admission and deletion across all its nodes.
type archiveReceipt struct {
	Snapshot         wire.SnapshotIdentity `json:"snapshot"`
	SHA256           string                `json:"sha256"`
	Size             int64                 `json:"size"`
	SourceTerminated bool                  `json:"source_terminated"`
	SourceStopped    bool                  `json:"source_stopped"`
	Deleted          bool                  `json:"deleted"`
}

func (b backend) storeDirectory() string {
	return filepath.Join(b.q.Config.CheckpointRoot, wire.Name(b.q.Config, b.q.Reference, 0))
}
func (b backend) archiveDirectory(operation string) string {
	return filepath.Join(b.storeDirectory(), "s-"+operation)
}
func privateDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return sandbox.ErrOwnership
	}
	return nil
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func durableJSON(path string, value any) error {
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".publish-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func readPrivateJSON(path string, value any) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return sandbox.ErrOwnership
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return e
	}
	if real != path {
		return sandbox.ErrOwnership
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if e = d.Decode(value); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return sandbox.ErrOwnership
	}
	return nil
}
func (b backend) receipt(operation string) (archiveReceipt, error) {
	var r archiveReceipt
	e := readPrivateJSON(filepath.Join(b.archiveDirectory(operation), "receipt.json"), &r)
	if e == nil && (r.Snapshot.OperationID != operation || wire.ValidateSnapshot(b.q.Config, b.q.Reference, r.Snapshot) != nil) {
		e = sandbox.ErrOwnership
	}
	if e == nil && !r.Deleted {
		decoded, err := hex.DecodeString(r.SHA256)
		if err != nil || len(decoded) != 32 || r.Size <= 0 || (r.SourceStopped && !r.SourceTerminated) {
			e = sandbox.ErrOwnership
		}
	}
	return r, e
}
func archiveDigest(path string) (string, int64, error) {
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", 0, e
	}
	if real != path {
		return "", 0, sandbox.ErrOwnership
	}
	st, e := os.Lstat(path)
	if e != nil {
		return "", 0, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return "", 0, sandbox.ErrOwnership
	}
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func (b backend) publishArchive(ctx context.Context, a *sdk.SnapshotArtifact, s wire.SnapshotIdentity) (archiveReceipt, error) {
	prior, e := b.receipt(s.OperationID)
	if e == nil {
		if prior.Snapshot != s || prior.Deleted {
			return prior, sandbox.ErrOwnership
		}
		return prior, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return prior, e
	}
	dir := b.archiveDirectory(s.OperationID)
	if e = privateDirectory(dir); e != nil {
		return prior, e
	}
	if e = syncDirectory(b.storeDirectory()); e != nil {
		return prior, e
	}
	archive := filepath.Join(dir, "archive.msb")
	// A publication interrupted before its receipt can be rebuilt from the exact
	// verified native artifact. The shared allocation lock excludes consumers.
	if e = os.Remove(archive); e != nil && !errors.Is(e, os.ErrNotExist) {
		return prior, e
	}
	if e = a.SaveTo(ctx, archive, sdk.SnapshotSaveOptions{WithImage: true}); e != nil {
		return prior, e
	}
	if e = os.Chmod(archive, 0600); e != nil {
		return prior, e
	}
	f, e := os.Open(archive)
	if e != nil {
		return prior, e
	}
	e = f.Sync()
	f.Close()
	if e != nil {
		return prior, e
	}
	digest, size, e := archiveDigest(archive)
	if e != nil {
		return prior, e
	}
	r := archiveReceipt{Snapshot: s, SHA256: digest, Size: size}
	return r, durableJSON(filepath.Join(dir, "receipt.json"), r)
}
func (b backend) importArchive(ctx context.Context, want wire.SnapshotIdentity) (*sdk.SnapshotArtifact, error) {
	r, e := b.receipt(want.OperationID)
	if e != nil {
		return nil, e
	}
	if r.Deleted || !r.SourceStopped || r.Snapshot != want {
		return nil, sandbox.ErrOwnership
	}
	class, e := wire.CheckpointClass(b.q.Config)
	if e != nil {
		return nil, e
	}
	if class != want.Compatibility {
		return nil, sandbox.ErrInvalid
	}
	path := filepath.Join(b.archiveDirectory(want.OperationID), "archive.msb")
	hash, size, e := archiveDigest(path)
	if e != nil {
		return nil, e
	}
	if hash != r.SHA256 || size != r.Size {
		return nil, sandbox.ErrOwnership
	}
	artifact, e := b.verifiedSnapshot(ctx, want)
	if sdk.IsKind(e, sdk.ErrSnapshotNotFound) {
		if _, e = sdk.Snapshot.LoadWithOptions(ctx, path, sdk.SnapshotLoadOptions{Group: wire.Name(b.q.Config, b.q.Reference, 0)}); e != nil {
			return nil, e
		}
		return b.verifiedSnapshot(ctx, want)
	}
	return artifact, e
}
func (b backend) deleteArchive(ctx context.Context, want wire.SnapshotIdentity) error {
	return b.deleteArchiveWithNative(want, func() error {
		_, err := b.verifiedSnapshot(ctx, want)
		if err == nil {
			err = sdk.Snapshot.Remove(ctx, want.Reference, false)
		}
		if sdk.IsKind(err, sdk.ErrSnapshotNotFound) {
			return nil
		}
		return err
	})
}

// Native artifact removal must settle before the private archive is unlinked.
func (b backend) deleteArchiveWithNative(want wire.SnapshotIdentity, removeNative func() error) error {
	if e := b.requireSettledRestores(nil, &want); e != nil {
		return e
	}
	r, e := b.receipt(want.OperationID)
	if errors.Is(e, os.ErrNotExist) {
		r = archiveReceipt{Snapshot: want}
		if e = privateDirectory(b.archiveDirectory(want.OperationID)); e != nil {
			return e
		}
		if e = syncDirectory(b.storeDirectory()); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if r.Snapshot != want {
		return sandbox.ErrOwnership
	}
	// Fence consumers before removing either local or portable artifact. Keep the
	// small tombstone, including its ownership identity, against delayed helpers.
	r.Deleted = true
	if e = durableJSON(filepath.Join(b.archiveDirectory(want.OperationID), "receipt.json"), r); e != nil {
		return e
	}
	if e = removeNative(); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(b.archiveDirectory(want.OperationID), "archive.msb")); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return syncDirectory(b.archiveDirectory(want.OperationID))
}

type restoreAdmission struct {
	OperationID string
	Snapshot    wire.SnapshotIdentity
	Target      wire.Compute
	Closed      bool
	CompletedID string
	Cleaned     bool
}

func (b backend) admitRestore(q wire.ResumeRequest) (bool, error) {
	if _, e := os.Lstat(filepath.Join(b.storeDirectory(), "k-"+q.Target.Name+".json")); e == nil {
		return false, wire.ErrUnconfirmed
	} else if !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
	want := restoreAdmission{OperationID: q.OperationID, Snapshot: q.Snapshot, Target: q.Target}
	want.Target.ID = ""
	var prior restoreAdmission
	e := readPrivateJSON(path, &prior)
	if e == nil {
		closed := prior.Closed
		prior.Closed = false
		prior.CompletedID = ""
		cleaned := prior.Cleaned
		prior.Cleaned = false
		if !reflect.DeepEqual(prior, want) {
			return false, sandbox.ErrOwnership
		}
		if closed {
			return true, nil
		}
		if cleaned {
			return false, wire.ErrUnconfirmed
		}
		if !q.ObserveOnly {
			return false, wire.ErrUnconfirmed
		}
		return false, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	if q.Target.ID != "" {
		return false, sandbox.ErrOwnership
	}
	want.Closed = q.ObserveOnly
	if e = durableJSON(path, want); e != nil {
		return false, e
	}
	return want.Closed, nil
}

// Only the current fresh dispatch, under the allocation lock and before its
// first RestoreSandbox call, may record this proof. A recovered open admission
// or native restore error cannot establish that no guest execution occurred.
func (b backend) closeExpiredRestore(q wire.ResumeRequest) error {
	if time.Now().Before(b.q.Deadline) {
		return nil
	}
	if q.ObserveOnly || q.Target.ID != "" {
		return sandbox.ErrOwnership
	}
	path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
	var prior restoreAdmission
	if err := readPrivateJSON(path, &prior); err != nil {
		return err
	}
	want := restoreAdmission{OperationID: q.OperationID, Snapshot: q.Snapshot, Target: q.Target}
	if !reflect.DeepEqual(prior, want) {
		return sandbox.ErrOwnership
	}
	want.Closed = true
	if err := durableJSON(path, want); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

// Completion records the incarnation whose native restore has become observable
// and whose full provenance and running state have been verified. This is
// execution evidence; cleanup separately fences dispatch and uses native locks.
func (b backend) completeRestore(ctx context.Context, q wire.ResumeRequest, target wire.Compute) (wire.State, error) {
	state, e := b.finishRestore(ctx, target)
	if e != nil {
		return state, e
	}
	path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
	var record restoreAdmission
	if e = readPrivateJSON(path, &record); e != nil {
		return state, e
	}
	if record.Closed || record.Snapshot != q.Snapshot || record.Target.Name != target.Name || record.Target.Generation != target.Generation {
		return state, sandbox.ErrOwnership
	}
	if record.CompletedID != "" && record.CompletedID != state.Compute.ID {
		return state, sandbox.ErrOwnership
	}
	record.CompletedID = state.Compute.ID
	return state, durableJSON(path, record)
}
func (b backend) requireSettledRestores(target *wire.Compute, snapshot *wire.SnapshotIdentity) error {
	files, e := os.ReadDir(b.storeDirectory())
	if e != nil {
		return e
	}
	for _, entry := range files {
		if !strings.HasPrefix(entry.Name(), "r-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var r restoreAdmission
		if e = readPrivateJSON(filepath.Join(b.storeDirectory(), entry.Name()), &r); e != nil {
			return e
		}
		if entry.Name() != "r-"+r.OperationID+".json" {
			return sandbox.ErrOwnership
		}
		if r.Closed || r.Cleaned {
			continue
		}
		if target != nil && (r.Target.Name != target.Name || r.Target.Generation != target.Generation) {
			continue
		}
		if snapshot != nil && r.Snapshot != *snapshot {
			continue
		}
		if r.CompletedID == "" {
			return wire.ErrUnconfirmed
		}
		if target != nil && target.ID == "" {
			target.ID = r.CompletedID
		}
		if target != nil && target.ID != "" && target.ID != r.CompletedID {
			return sandbox.ErrOwnership
		}
	}
	return nil
}

func (b backend) verifyArchive(r archiveReceipt) error {
	hash, size, e := archiveDigest(filepath.Join(b.archiveDirectory(r.Snapshot.OperationID), "archive.msb"))
	if e != nil {
		return e
	}
	if hash != r.SHA256 || size != r.Size {
		return sandbox.ErrOwnership
	}
	return nil
}

// The native transition/lifecycle locks fence an admitted launcher and its
// detached child. This receipt means cleanup completed, never "did not run".
func (b backend) recordRestoreCleanup(target wire.Compute) error {
	files, e := os.ReadDir(b.storeDirectory())
	if e != nil {
		return e
	}
	for _, entry := range files {
		if !strings.HasPrefix(entry.Name(), "r-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(b.storeDirectory(), entry.Name())
		var r restoreAdmission
		if e = readPrivateJSON(path, &r); e != nil {
			return e
		}
		if entry.Name() != "r-"+r.OperationID+".json" {
			return sandbox.ErrOwnership
		}
		if r.Target.Name != target.Name || r.Target.Generation != target.Generation || r.Closed || r.Cleaned {
			continue
		}
		if target.ID != "" && r.CompletedID != "" && target.ID != r.CompletedID {
			return sandbox.ErrOwnership
		}
		r.Cleaned = true
		if e = durableJSON(path, r); e != nil {
			return e
		}
	}
	return nil
}

func (b backend) pinRestoreCleanup(target *wire.Compute) error {
	files, e := os.ReadDir(b.storeDirectory())
	if e != nil {
		return e
	}
	for _, entry := range files {
		if !strings.HasPrefix(entry.Name(), "r-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var r restoreAdmission
		if e = readPrivateJSON(filepath.Join(b.storeDirectory(), entry.Name()), &r); e != nil {
			return e
		}
		if entry.Name() != "r-"+r.OperationID+".json" {
			return sandbox.ErrOwnership
		}
		if r.Target.Name != target.Name || r.Target.Generation != target.Generation || r.Closed {
			continue
		}
		if target.RestoredFrom == nil || *target.RestoredFrom != r.Snapshot {
			return sandbox.ErrOwnership
		}
		if r.CompletedID != "" {
			if target.ID != "" && target.ID != r.CompletedID {
				return sandbox.ErrOwnership
			}
			target.ID = r.CompletedID
		}
	}
	return nil
}
