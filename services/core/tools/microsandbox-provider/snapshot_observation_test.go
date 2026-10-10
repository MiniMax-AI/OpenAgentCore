//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

// These requests use the real SDK's empty local registry, without starting a VM.
func TestPermanentRestoreImportFailureKeepsOneAdmission(t *testing.T) {
	if !isolatedSDKTest(t) {
		return
	}
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			request := initialInfoRequest(t)
			request.Config.RuntimeHome = home
			guard, err := allocationLock(request)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			b := backend{q: request}
			q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: wire.Name(request.Config, request.Reference, 1), Generation: 1}}
			if damage == "corrupt" {
				compatibility, err := wire.CheckpointClass(request.Config)
				if err != nil {
					t.Fatal(err)
				}
				q.Snapshot = wire.SnapshotIdentity{Compatibility: compatibility, Reference: wire.SnapshotReference(request.Config, request.Reference, q.OperationID), ID: "id", Digest: "digest", CheckpointID: "checkpoint", CheckpointRoot: "root", OperationID: q.OperationID, SourceName: wire.Name(request.Config, request.Reference, 0), SourceID: "local:source"}
				dir := b.archiveDirectory(q.OperationID)
				if err = privateDirectory(dir); err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(dir, "archive.msb")
				if err = os.WriteFile(archive, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				hash, size, err := archiveDigest(archive)
				if err != nil {
					t.Fatal(err)
				}
				if err = durableJSON(filepath.Join(dir, "receipt.json"), archiveReceipt{Snapshot: q.Snapshot, SHA256: hash, Size: size, SourceStopped: true, SourceTerminated: true}); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(archive, []byte("corrupt!"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = b.resume(t.Context(), q); err == nil {
				t.Fatal("missing archive accepted")
			}
			path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
			var record restoreAdmission
			if err = readPrivateJSON(path, &record); err != nil || record.Closed {
				t.Fatal("permanent failure closed", record, err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(b.storeDirectory())
			if err != nil {
				t.Fatal(err)
			}
			q.ObserveOnly = true
			for i := 0; i < 3; i++ {
				state, err := b.resume(t.Context(), q)
				if !errors.Is(err, wire.ErrUnconfirmed) || state.RestoreAttemptClosed != "" {
					t.Fatal(state, err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("journal changed", err)
			}
			afterFiles, err := os.ReadDir(b.storeDirectory())
			if err != nil || len(afterFiles) != len(files) {
				t.Fatal("journal grew", err)
			}
		})
	}
}

func TestFreshRestoreRequestDeadlineClosesBeforeNativeExecution(t *testing.T) {
	if !isolatedSDKTest(t) {
		return
	}
	request := initialInfoRequest(t)
	t.Setenv("MSB_HOME", request.Config.RuntimeHome)
	guard, err := allocationLock(request)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	// The real request acquired its lock before its deadline. Time can expire
	// while prior SDK work settles; the lifecycle context deliberately stays live.
	request.Deadline = time.Now().Add(-time.Second)
	q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: wire.Name(request.Config, request.Reference, 1), Generation: 1}}
	request.Operation = "resume"
	request.Resume = &q
	b := backend{q: request}
	_, err = b.run(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("request deadline not enforced", err)
	}
	q.ObserveOnly = true
	state, err := b.resume(context.Background(), q)
	if err != nil || state.RestoreAttemptClosed != q.OperationID || state.Status != "absent" {
		t.Fatal("closed proof missing", state, err)
	}
	q.ObserveOnly = false
	if _, err = b.resume(context.Background(), q); !errors.Is(err, wire.ErrUnconfirmed) {
		t.Fatal("late request replayed", err)
	}
}

// The official SDK freezes its default backend on first use. Match production's
// one-request helper process rather than changing MSB_HOME under a cached pool.
func isolatedSDKTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv("OAC_NATIVE_TEST_CHILD") == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), "OAC_NATIVE_TEST_CHILD="+t.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated SDK test: %v\n%s", err, output)
	}
	return false
}

func TestAdmittedAbsentObservationSkipsArchiveAndStaysUnknown(t *testing.T) {
	if !isolatedSDKTest(t) {
		return
	}
	request := initialInfoRequest(t)
	t.Setenv("MSB_HOME", request.Config.RuntimeHome)
	guard, err := allocationLock(request)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	b := backend{q: request}
	q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: wire.Name(request.Config, request.Reference, 1), Generation: 1}}
	if _, err = b.admitRestore(q); err != nil {
		t.Fatal(err)
	}
	// Even a missing archive must not be consulted: its ENOENT would differ from
	// the typed unknown result returned for this existing admitted target.
	path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b.q.Deadline = time.Now().Add(-time.Second)
	q.ObserveOnly = true
	state, err := b.resume(t.Context(), q)
	if !errors.Is(err, wire.ErrUnconfirmed) || state.RestoreAttemptClosed != "" {
		t.Fatal(state, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("observation mutated admission", err)
	}
	q.ObserveOnly = false
	if _, err = b.resume(t.Context(), q); !errors.Is(err, wire.ErrUnconfirmed) {
		t.Fatal("old admission replayed", err)
	}
}

func TestPreDispatchClosureRejectsConflictingJournal(t *testing.T) {
	for _, kind := range []string{"observe", "completed", "cleaned", "foreign", "missing"} {
		t.Run(kind, func(t *testing.T) {
			request := initialInfoRequest(t)
			guard, err := allocationLock(request)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			request.Deadline = time.Now().Add(-time.Second)
			b := backend{q: request}
			q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: "target", Generation: 1}}
			r := restoreAdmission{OperationID: q.OperationID, Target: q.Target, Snapshot: q.Snapshot}
			switch kind {
			case "observe":
				q.ObserveOnly = true
			case "completed":
				r.CompletedID = "local:1"
			case "cleaned":
				r.Cleaned = true
			case "foreign":
				r.Target.Name = "foreign"
			}
			path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
			if kind != "missing" {
				if err = durableJSON(path, r); err != nil {
					t.Fatal(err)
				}
			}
			before, readErr := os.ReadFile(path)
			wantErr := sandbox.ErrOwnership
			if kind == "missing" {
				wantErr = os.ErrNotExist
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("expected missing journal", readErr)
				}
			} else if readErr != nil {
				t.Fatal(readErr)
			}
			if err = b.closeExpiredRestore(q); !errors.Is(err, wantErr) {
				t.Fatalf("unsafe closure result: got %v, want %v", err, wantErr)
			}
			after, readErr := os.ReadFile(path)
			if kind == "missing" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("missing journal was created", readErr)
				}
			} else if readErr != nil || string(after) != string(before) {
				t.Fatal("rejected closure changed journal", readErr)
			}
		})
	}
}

func TestFreshAdmissionClosesOnlyAfterRequestDeadline(t *testing.T) {
	request := initialInfoRequest(t)
	guard, err := allocationLock(request)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	b := backend{q: request}
	q := wire.ResumeRequest{OperationID: "66666666-6666-4666-8666-666666666666", Target: wire.Compute{Name: "target", Generation: 1}}
	if _, err = b.admitRestore(q); err != nil {
		t.Fatal(err)
	}
	if err = b.closeExpiredRestore(q); err != nil {
		t.Fatal("live request was closed", err)
	}
	var r restoreAdmission
	path := filepath.Join(b.storeDirectory(), "r-"+q.OperationID+".json")
	if err = readPrivateJSON(path, &r); err != nil || r.Closed {
		t.Fatal(r, err)
	}
	b.q.Deadline = time.Now().Add(-time.Second)
	if err = b.closeExpiredRestore(q); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err = readPrivateJSON(path, &r); err != nil || !r.Closed {
		t.Fatal("expired dispatch not fenced", r, err)
	}
}
