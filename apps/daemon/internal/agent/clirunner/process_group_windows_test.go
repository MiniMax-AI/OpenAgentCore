//go:build windows

package clirunner

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWindowsOwnedTree(t *testing.T) {
	for _, mode := range []string{"cancel", "parent-cancel", "leader-exit"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p, err := Start(StartOptions{Parent: ctx, Binary: os.Args[0], Args: []string{"-test.run=^TestWindowsTreeHelper$", "--", "leader", mode, dir}, Env: append(os.Environ(), "OAC_TREE_HELPER=1"), OwnProcessGroup: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { p.Cancel(); _ = p.Wait() }()
			go io.Copy(io.Discard, p.Stdout)
			go io.Copy(io.Discard, p.Stderr)
			child := waitWindowsPID(t, filepath.Join(dir, "child"))
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(child))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			switch mode {
			case "cancel":
				p.Cancel()
				p.Cancel()
			case "parent-cancel":
				cancel()
			case "leader-exit":
				os.WriteFile(filepath.Join(dir, "exit"), nil, 0600)
			}
			done := make(chan struct{})
			go func() { _ = p.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("owned tree did not settle")
			}
			if status, err := windows.WaitForSingleObject(handle, 0); err != nil || status != windows.WAIT_OBJECT_0 {
				t.Fatal("descendant survived confirmed cleanup", status, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "late")); !os.IsNotExist(err) {
				t.Fatal("descendant wrote after cleanup")
			}
		})
	}
}

func TestWindowsJobClosesAfterOwnerCrash(t *testing.T) {
	dir := t.TempDir()
	owner := exec.Command(os.Args[0], "-test.run=^TestWindowsTreeHelper$", "--", "owner", "cancel", dir)
	owner.Env = append(os.Environ(), "OAC_TREE_HELPER=1")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	child := waitWindowsPID(t, filepath.Join(dir, "child"))
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(child))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err = owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if status, err := windows.WaitForSingleObject(handle, 5000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("Job descendant survived owner crash", status, err)
	}
}

func waitWindowsPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(path)
		pid, err := strconv.Atoi(string(raw))
		if err == nil && pid > 0 {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child did not start")
	return 0
}

func TestWindowsTreeHelper(t *testing.T) {
	if os.Getenv("OAC_TREE_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 4 {
		os.Exit(2)
	}
	role, mode, dir := args[1], args[2], args[3]
	switch role {
	case "child":
		_ = os.WriteFile(filepath.Join(dir, "child"), []byte(strconv.Itoa(os.Getpid())), 0600)
		time.Sleep(10 * time.Second)
		_ = os.WriteFile(filepath.Join(dir, "late"), nil, 0600)
	case "leader":
		c := exec.Command(os.Args[0], "-test.run=^TestWindowsTreeHelper$", "--", "child", mode, dir)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "exit")); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
	case "owner":
		p, err := Start(StartOptions{Parent: context.Background(), Binary: os.Args[0], Args: []string{"-test.run=^TestWindowsTreeHelper$", "--", "leader", mode, dir}, Env: os.Environ(), OwnProcessGroup: true})
		if err != nil {
			os.Exit(3)
		}
		_ = p.Wait()
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
