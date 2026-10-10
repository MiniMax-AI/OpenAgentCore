//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllocationLockSurvivesCallerDeadlineUntilExplicitSettlement(t *testing.T) {
	home := t.TempDir()
	q := wire.Request{Config: wire.Config{RuntimeHome: home}, Deadline: time.Now().Add(time.Second)}
	release, e := allocationLock(q)
	if e != nil {
		t.Fatal(e)
	}
	waiting := q
	waiting.Deadline = time.Now().Add(40 * time.Millisecond)
	if next, e := allocationLock(waiting); !errors.Is(e, context.DeadlineExceeded) {
		if next != nil {
			next.Close()
		}
		t.Fatalf("second lifecycle entered: %v", e)
	}
	release.Close()
	q.Deadline = time.Now().Add(time.Second)
	release, e = allocationLock(q)
	if e != nil {
		t.Fatal(e)
	}
	release.Close()
}
func TestLockDirectoryCannotRedirectIntoAnotherHome(t *testing.T) {
	home := t.TempDir()
	foreign := t.TempDir()
	if e := os.Symlink(foreign, filepath.Join(home, "oac-locks")); e != nil {
		t.Fatal(e)
	}
	_, e := allocationLock(wire.Request{Config: wire.Config{RuntimeHome: home}, Deadline: time.Now().Add(time.Second)})
	if e == nil {
		t.Fatal("symlink lock directory accepted")
	}
}

func initialInfoRequest(t *testing.T) wire.Request {
	t.Helper()
	config, _ := deploymentFixture()
	config.InstallationID = "11111111-1111-4111-8111-111111111111"
	config.HelperPath, config.RuntimePath, config.FirmwarePath = "/helper", "/runtime", "/firmware"
	config.RuntimeHome = t.TempDir()
	config.RuntimeSHA256, config.FirmwareSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	config.Network = wire.NetworkPolicy{DefaultEgress: "deny", DefaultIngress: "deny"}
	ref := sandbox.Reference{TenantID: "22222222-2222-4222-8222-222222222222", EnvironmentID: "33333333-3333-4333-8333-333333333333", AllocationID: "44444444-4444-4444-8444-444444444444"}
	return wire.Request{Version: wire.ProtocolVersion, Operation: "initial_info", Config: config, Reference: ref,
		Compute: wire.Compute{Name: wire.Name(config, ref, 0)}, Deadline: time.Now().Add(5 * time.Second)}
}

func TestInitialAbsencePreventsLateCreateAndSurvivesReopen(t *testing.T) {
	q := initialInfoRequest(t)
	guard, err := allocationLock(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.admitCreate(); err != nil {
		t.Fatal("new allocation refused", err)
	}
	// A detached Create helper can still be alive before acquiring the lock,
	// including across node restart and before its original deadline expires.
	late := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		create, err := allocationLock(q)
		if err == nil {
			err = create.admitCreate()
			create.Close()
		}
		late <- err
	}()
	<-started
	out, err := guard.settleInitialAbsence(q, &sdk.Error{Kind: sdk.ErrSandboxNotFound})
	if err != nil || !out.CreateSettled || out.State == nil || *out.State != (wire.State{Compute: q.Compute, Status: "absent"}) {
		t.Fatal("no exact absence receipt", out, err)
	}
	guard.Close()
	if err := <-late; !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("late Create admitted", err)
	}
	// A fresh descriptor has no in-memory knowledge of the observing process.
	reopened, err := allocationLock(q)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.admitCreate(); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("restart lost admission closure", err)
	}
}

func TestOnlyExactInitialNativeAbsenceClosesAdmission(t *testing.T) {
	for _, name := range []string{"ordinary inspect", "foreign name", "known ID", "restored", "later generation", "bare not found", "unknown"} {
		t.Run(name, func(t *testing.T) {
			q := initialInfoRequest(t)
			guard, err := allocationLock(q)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			nativeErr := error(&sdk.Error{Kind: sdk.ErrSandboxNotFound})
			switch name {
			case "ordinary inspect":
				q.Operation = "inspect"
			case "foreign name":
				q.Compute.Name = "foreign"
			case "known ID":
				q.Compute.ID = "native:id"
			case "restored":
				q.Compute.RestoredFrom = &wire.SnapshotIdentity{}
			case "later generation":
				q.Compute.Generation = 1
			case "bare not found":
				nativeErr = sandbox.ErrNotFound
			case "unknown":
				nativeErr = wire.ErrUnconfirmed
			}
			out, err := guard.settleInitialAbsence(q, nativeErr)
			if err == nil || out.CreateSettled {
				t.Fatal("unproven absence settled", out, err)
			}
			if err := guard.admitCreate(); err != nil {
				t.Fatal("observation closed admission", err)
			}
		})
	}
}

func TestInvalidOrUnwritableAdmissionStateFailsClosed(t *testing.T) {
	for _, content := range []string{"c", "closed", "closed\nextra", "unknown"} {
		t.Run(content, func(t *testing.T) {
			q := initialInfoRequest(t)
			guard, err := allocationLock(q)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			if _, err := guard.file.WriteAt([]byte(content), 0); err != nil {
				t.Fatal(err)
			}
			if err := guard.admitCreate(); err == nil {
				t.Fatal("invalid marker admitted Create")
			}
			out, err := guard.settleInitialAbsence(q, &sdk.Error{Kind: sdk.ErrSandboxNotFound})
			if err == nil || out.CreateSettled {
				t.Fatal("invalid marker settled", out, err)
			}
		})
	}
	t.Run("sync error", func(t *testing.T) {
		q := initialInfoRequest(t)
		file, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		out, err := (&allocationGuard{file: file}).settleInitialAbsence(q, &sdk.Error{Kind: sdk.ErrSandboxNotFound})
		if err == nil || out.CreateSettled {
			t.Fatal("failed marker sync settled", out, err)
		}
	})
	t.Run("write error", func(t *testing.T) {
		q := initialInfoRequest(t)
		guard, err := allocationLock(q)
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Close()
		readonly, err := os.Open(guard.file.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer readonly.Close()
		out, err := (&allocationGuard{file: readonly}).settleInitialAbsence(q, &sdk.Error{Kind: sdk.ErrSandboxNotFound})
		if err == nil || out.CreateSettled {
			t.Fatal("failed marker write settled", out, err)
		}
	})
}
