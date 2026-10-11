//go:build linux

package nfs

import (
	"context"
	"errors"
	"os"
	"path"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

func local(t *testing.T) (*Filesystem, *os.Root, workspacefs.Reference) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err = r.Mkdir("objects", 0700); err != nil {
		t.Fatal(err)
	}
	return &Filesystem{parameters: parameters{UID: uint32(os.Geteuid())}}, r, workspacefs.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), ObjectID: uuid.NewString()}
}
func TestLifecycleConcurrentReplay(t *testing.T) {
	a, r, ref := local(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() { errs <- a.create(r, ref, nil) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data := path.Join(object(ref), "live", "data", "valuable")
	if err := write(r, data, []byte("retain")); err != nil {
		t.Fatal(err)
	}
	if err := a.create(r, ref, nil); err != nil {
		t.Fatal(err)
	}
	if raw, err := r.ReadFile(data); err != nil || string(raw) != "retain" {
		t.Fatalf("data overwritten %s %v", raw, err)
	}
	errs = make(chan error, 16)
	for range 16 {
		wg.Go(func() { errs <- a.delete(r, ref) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := a.create(r, ref, nil); !errors.Is(err, workspacefs.ErrNotFound) {
		t.Fatalf("retired create: %v", err)
	}
	if err := live(r, ref); !errors.Is(err, workspacefs.ErrNotFound) {
		t.Fatalf("retired observe: %v", err)
	}
	if _, err := r.Stat(path.Join(object(ref), "identity")); err != nil {
		t.Fatal(err)
	}
}
func TestForeignIdentityNeverDeletes(t *testing.T) {
	a, r, ref := local(t)
	if err := a.create(r, ref, nil); err != nil {
		t.Fatal(err)
	}
	other := ref
	other.TenantID = uuid.NewString()
	if err := a.delete(r, other); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatal(err)
	}
	if err := live(r, ref); err != nil {
		t.Fatal(err)
	}
}
func TestMissingMarkerDoesNotDeleteData(t *testing.T) {
	a, r, ref := local(t)
	if err := a.create(r, ref, nil); err != nil {
		t.Fatal(err)
	}
	dir := path.Join(object(ref), "live")
	if err := write(r, path.Join(dir, "data", "valuable"), []byte("keep")); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(path.Join(dir, "identity")); err != nil {
		t.Fatal(err)
	}
	if err := removeEnvelope(r, dir, ref, true); err == nil {
		t.Fatal("removed data without identity")
	}
	if _, err := r.Stat(path.Join(dir, "data", "valuable")); err != nil {
		t.Fatal(err)
	}
}
func TestCheckRejectsLocalFilesystem(t *testing.T) {
	a := &Filesystem{parameters: parameters{Root: t.TempDir(), UID: 65532}}
	if err := a.Check(context.Background()); !errors.Is(err, workspacefs.ErrUnavailable) {
		t.Fatalf("local path accepted: %v", err)
	}
}

func TestCreateDoesNotReplaceUnownedLive(t *testing.T) {
	a, r, ref := local(t)
	if err := a.claim(r, ref); err != nil {
		t.Fatal(err)
	}
	if err := r.Mkdir(path.Join(object(ref), "live"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.create(r, ref, nil); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatalf("unowned live: %v", err)
	}
}

func TestObserveAbsentObjectsDoesNotPrepareNamespace(t *testing.T) {
	a, r, ref := local(t)
	if err := r.Remove("objects"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := a.observe(r, ref); !errors.Is(err, workspacefs.ErrNotFound) {
			t.Fatalf("absent object: %v", err)
		}
		dir, err := r.Open(".")
		if err != nil {
			t.Fatal(err)
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("Observe created namespace entries: %v", entries)
		}
	}
	// Delete of a never-created reference still persists terminal ownership.
	if err := a.delete(r, ref); err != nil {
		t.Fatal(err)
	}
	if err := a.create(r, ref, nil); !errors.Is(err, workspacefs.ErrNotFound) {
		t.Fatalf("deleted identity reused: %v", err)
	}
}

func TestQuotaBoundPrecedesStageAndEveryRetry(t *testing.T) {
	a, r, ref := local(t)
	calls := 0
	failure := workspacefs.ErrUnconfirmed
	bound := func() error {
		calls++
		entries, err := os.ReadDir(path.Join(r.Name(), object(ref), "staging"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("bound after a stage was created: %v %v", entries, err)
		}
		if _, err := r.Lstat(path.Join(object(ref), "live")); err == nil && calls == 1 {
			t.Fatal("bound after publication")
		}
		return failure
	}
	if err := a.create(r, ref, bound); !errors.Is(err, workspacefs.ErrUnconfirmed) || calls != 1 {
		t.Fatalf("unbounded creation continued: %v", err)
	}
	if _, err := r.Lstat(path.Join(object(ref), "live")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published without a verified bound", err)
	}
	failure = nil
	if err := a.create(r, ref, bound); err != nil || calls != 2 {
		t.Fatal(err)
	}
	if err := a.create(r, ref, bound); err != nil || calls != 3 {
		t.Fatal("published object retry skipped verification", err)
	}
	if err := a.delete(r, ref); err != nil {
		t.Fatal(err)
	}
	if err := a.create(r, ref, bound); !errors.Is(err, workspacefs.ErrNotFound) || calls != 3 {
		t.Fatal("deleted object reached the quota server", err)
	}
}

func TestCreateRejectsCapacityWithoutQuota(t *testing.T) {
	a := &Filesystem{parameters: parameters{Root: t.TempDir(), UID: 65532}}
	ref := workspacefs.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), ObjectID: uuid.NewString()}
	if _, err := a.Create(context.Background(), ref, 1024); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal(err)
	}
}
