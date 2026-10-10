//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func TestRestoreObservationClosesOnlyNeverAdmittedOperation(t *testing.T) {
	request := initialInfoRequest(t)
	guard, err := allocationLock(request)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	b := backend{q: request}
	q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: "exact-target", Generation: 1}, ObserveOnly: true}
	closed, err := b.admitRestore(q)
	if err != nil || !closed {
		t.Fatal(closed, err)
	}
	q.ObserveOnly = false
	// A late helper cannot replace the durable closed record with admission.
	closed, err = b.admitRestore(q)
	if err != nil || !closed {
		t.Fatal("late restore admitted", closed, err)
	}
	q.OperationID = "77777777-7777-4777-8777-777777777777"
	closed, err = b.admitRestore(q)
	if err != nil || closed {
		t.Fatal(closed, err)
	}
	// A crash after admission remains unknown, including when no target is yet
	// visible. The helper never manufactures no-effect evidence from absence.
	q.ObserveOnly = true
	closed, err = b.admitRestore(q)
	if err != nil || closed {
		t.Fatal("admitted operation closed", closed, err)
	}
	q.ObserveOnly = false
	if _, err = b.admitRestore(q); !errors.Is(err, wire.ErrUnconfirmed) {
		t.Fatal("admitted operation replayed", err)
	}
	q.ObserveOnly = true
	q.Target.Name = "foreign"
	if _, err = b.admitRestore(q); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal("foreign target admitted", err)
	}
}
func TestRestoreJournalRejectsPartialAndSymlinkRecords(t *testing.T) {
	for _, kind := range []string{"partial", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			request := initialInfoRequest(t)
			guard, err := allocationLock(request)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			b := backend{q: request}
			q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", ObserveOnly: true}
			path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
			if kind == "partial" {
				err = os.WriteFile(path, []byte("{"), 0600)
			} else {
				err = os.Symlink(filepath.Join(t.TempDir(), "missing"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.admitRestore(q); err == nil {
				t.Fatal("unsafe record accepted")
			}
		})
	}
}
func TestArchiveDigestRejectsGuestReadableAndSymlinkArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive")
	if err := os.WriteFile(path, []byte("private RAM"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, size, err := archiveDigest(path)
	if err != nil || len(hash) != 64 || size != 11 {
		t.Fatal(hash, size, err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = archiveDigest(path); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err = archiveDigest(link); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
}

func TestAdmittedRestoreBlocksCleanupUntilExactCompletion(t *testing.T) {
	request := initialInfoRequest(t)
	guard, err := allocationLock(request)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	b := backend{q: request}
	q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: "exact-target", Generation: 1}}
	if _, err = b.admitRestore(q); err != nil {
		t.Fatal(err)
	}
	if err = b.requireSettledRestores(&q.Target, nil); !errors.Is(err, wire.ErrUnconfirmed) {
		t.Fatal("unknown target was releasable", err)
	}
	if err = b.requireSettledRestores(nil, &q.Snapshot); !errors.Is(err, wire.ErrUnconfirmed) {
		t.Fatal("unknown artifact was releasable", err)
	}
	other := wire.Compute{Name: "other-source"}
	if err = b.requireSettledRestores(&other, nil); err != nil {
		t.Fatal("unrelated source cleanup blocked", err)
	}
	record := restoreAdmission{OperationID: q.OperationID, Target: q.Target, Snapshot: q.Snapshot, CompletedID: "local:exact"}
	if err = durableJSON(filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json"), record); err != nil {
		t.Fatal(err)
	}
	if err = b.requireSettledRestores(&q.Target, nil); err != nil || q.Target.ID != "local:exact" {
		t.Fatal("cleanup did not pin completed incarnation", q.Target, err)
	}
	q.Target.ID = "local:foreign"
	if err = b.requireSettledRestores(&q.Target, nil); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal("foreign cleanup accepted", err)
	}
}

func TestTargetCleanupFenceSurvivesReopenBeforeAndAfterAdmission(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(fmt.Sprint(admitted), func(t *testing.T) {
			request := initialInfoRequest(t)
			guard, err := allocationLock(request)
			if err != nil {
				t.Fatal(err)
			}
			b := backend{q: request}
			q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: "exact-target", Generation: 1}}
			if admitted {
				if _, err = b.admitRestore(q); err != nil {
					t.Fatal(err)
				}
			}
			if err = durableJSON(filepath.Join(b.storeDirectory(), "k-"+q.Target.Name+".json"), q.Target); err != nil {
				t.Fatal(err)
			}
			guard.Close()
			guard, err = allocationLock(request)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			if _, err = b.admitRestore(q); !errors.Is(err, wire.ErrUnconfirmed) {
				t.Fatal("late restore crossed cleanup fence", err)
			}
			if admitted {
				// Only the caller which has completed native Get/Kill/Remove/Get invokes
				// this journal update. The live crash probe qualifies the native fence.
				if err = b.recordRestoreCleanup(q.Target); err != nil {
					t.Fatal(err)
				}
				if err = b.requireSettledRestores(nil, &q.Snapshot); err != nil {
					t.Fatal("cleaned obligation retained", err)
				}
			}
		})
	}
}

func TestLostCaptureResponseStillExposesOwnedIdentityWhenArchiveIsDamaged(t *testing.T) {
	for _, settled := range []bool{false, true} {
		for _, damage := range []string{"corrupt", "missing"} {
			t.Run(fmt.Sprintf("settled=%t/%s", settled, damage), func(t *testing.T) {
				request := initialInfoRequest(t)
				guard, err := allocationLock(request)
				if err != nil {
					t.Fatal(err)
				}
				defer guard.Close()
				b := backend{q: request}
				source := request.Compute
				source.ID = "local:known-source"
				operation := "66666666-6666-4666-8666-666666666666"
				compatibility, err := wire.CheckpointClass(request.Config)
				if err != nil {
					t.Fatal(err)
				}
				snapshot := wire.SnapshotIdentity{Compatibility: compatibility, Reference: wire.SnapshotReference(request.Config, request.Reference, operation), ID: "exact-snapshot", Digest: "digest", CheckpointID: "checkpoint", CheckpointRoot: "root", OperationID: operation, SourceName: source.Name, SourceID: source.ID}
				dir := b.archiveDirectory(operation)
				if err = privateDirectory(dir); err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(dir, "archive.msb")
				if err = os.WriteFile(archive, []byte("verified full archive"), 0600); err != nil {
					t.Fatal(err)
				}
				hash, size, err := archiveDigest(archive)
				if err != nil {
					t.Fatal(err)
				}
				receipt := archiveReceipt{Snapshot: snapshot, SHA256: hash, Size: size, SourceTerminated: settled, SourceStopped: settled}
				if err = durableJSON(filepath.Join(dir, "receipt.json"), receipt); err != nil {
					t.Fatal(err)
				}
				if damage == "corrupt" {
					err = os.WriteFile(archive, []byte("damaged"), 0600)
				} else {
					err = os.Remove(archive)
				}
				if err != nil {
					t.Fatal(err)
				}
				// Core lost the capture response and therefore has no Snapshot yet. It
				// needs this ownership identity to reach ordinary Kill/DeleteSnapshot.
				state, err := b.suspend(t.Context(), wire.SuspendRequest{Reference: request.Reference, OperationID: operation, Source: source, ObserveOnly: true})
				if err != nil || state.Snapshot == nil || *state.Snapshot != snapshot || state.Compute != source || state.SourceStopped || state.Status != "unknown" || state.BootstrapComplete {
					t.Fatalf("cleanup identity hidden or execution authorized: %+v %v", state, err)
				}
				if artifact, err := b.importArchive(t.Context(), snapshot); err == nil || artifact != nil {
					t.Fatal("damaged archive was usable for restore", err)
				}
				if _, err = b.suspend(t.Context(), wire.SuspendRequest{Reference: request.Reference, OperationID: operation, Source: source}); err == nil {
					t.Fatal("non-observation accepted unusable archive")
				}
				foreign := source
				foreign.ID = "local:foreign"
				denied, err := b.suspend(t.Context(), wire.SuspendRequest{Reference: request.Reference, OperationID: operation, Source: foreign, ObserveOnly: true})
				if !errors.Is(err, sandbox.ErrOwnership) || denied.Snapshot != nil {
					t.Fatal("foreign request recovered owned identity", denied, err)
				}
				nativeRemoved := false
				if err = b.deleteArchiveWithNative(*state.Snapshot, func() error { nativeRemoved = true; return nil }); err != nil {
					t.Fatal("owned damaged archive could not be deleted", err)
				}
				if !nativeRemoved {
					t.Fatal("native cleanup was skipped")
				}
				if _, err = os.Lstat(archive); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("archive remains", err)
				}
				deleted, err := b.receipt(operation)
				if err != nil || !deleted.Deleted || deleted.Snapshot != snapshot {
					t.Fatal("exact deletion fence missing", deleted, err)
				}
			})
		}
	}
}
