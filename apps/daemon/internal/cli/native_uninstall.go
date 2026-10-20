package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// runUninstall removes one native installation: it stops the daemon, shows what
// goes, asks, deletes. The workspace belongs to the user and always stays.
func runUninstall(rc *runContext, args []string) error {
	flags := newFlagSet("uninstall")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("uninstall: takes no arguments; run the installed bin/oac-daemon uninstall")
	}
	root, err := paths.Root()
	if err != nil {
		return err
	}
	raw, err := runtimefs.ReadPrivatePath(filepath.Join(root, "daemon", "installation.json"), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(rc.stdout, "oac-daemon: nothing is installed in %s\n", root)
		return nil
	}
	if err != nil {
		return errors.New("uninstall: cannot read the installation; delete its directory by hand")
	}
	// A damaged record still allows removal; only the workspace check and the
	// follow-up need its contents.
	var config nativeInstallation
	_ = decodeEnvironmentJSON(raw, &config)
	if config.Workspace != "" && within(root, config.Workspace) {
		return fmt.Errorf("uninstall: the workspace %s is inside the installation directory; move it out first", config.Workspace)
	}
	var owned []string
	for _, name := range nativeInstallationEntries {
		if _, err = os.Lstat(filepath.Join(root, name)); err == nil {
			owned = append(owned, filepath.Join(root, name))
		}
	}
	foreign, err := foreignEntries(root)
	if err != nil {
		return errors.New("uninstall: cannot read the installation directory; delete its entries by hand")
	}
	fmt.Fprintln(rc.stdout, "This stops the daemon and deletes, with everything inside:")
	for _, target := range owned {
		fmt.Fprintln(rc.stdout, "  "+target)
	}
	fmt.Fprintln(rc.stdout, "These hold the executor credential, native history and Harnesses. The workspace stays, and so does anything else in "+root+" and any credential or capability directory outside the installation.")
	fmt.Fprint(rc.stdout, "Continue? [y/N] ")
	answer, _ := bufio.NewReader(rc.stdin).ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return errors.New("uninstall: cancelled; nothing was deleted")
	}
	if err = runStop(rc, nil); err != nil {
		return err
	}
	// Windows cannot delete the executable it is running.
	var running os.FileInfo
	if runtime.GOOS == "windows" {
		if exe, e := os.Executable(); e == nil {
			running, _ = os.Stat(exe)
		}
	}
	// The record and the executable go last: a failed uninstall can run again.
	order := owned[:0:0]
	for _, target := range owned {
		if name := filepath.Base(target); name != "bin" && name != "daemon" {
			order = append(order, target)
		}
	}
	record := filepath.Join(root, "daemon", "installation.json")
	if entries, e := os.ReadDir(filepath.Join(root, "daemon")); e == nil {
		for _, entry := range entries {
			if path := filepath.Join(root, "daemon", entry.Name()); path != record {
				order = append(order, path)
			}
		}
	}
	bin := filepath.Join(root, "bin")
	order = append(order, bin, record)
	var kept bool
	for _, target := range order {
		held, e := removeTree(target, running)
		if e != nil {
			return fmt.Errorf("uninstall: cannot delete %s; close programs using it and run the command again: %w", target, e)
		}
		kept = kept || held
	}
	// Only empty directories go; anything left belongs to the user.
	_ = os.Remove(filepath.Join(root, "daemon"))
	if kept {
		// Windows keeps the running executable open until this process exits.
		deleteAfterExit(root)
	} else {
		_ = os.Remove(root)
	}
	fmt.Fprintln(rc.stdout, "oac-daemon: uninstalled")
	if config.Workspace != "" {
		fmt.Fprintln(rc.stdout, "Workspace kept: "+config.Workspace)
	}
	for _, name := range foreign {
		fmt.Fprintln(rc.stdout, "Kept: "+filepath.Join(root, name))
	}
	for _, path := range []string{config.Credential, config.CapabilityDirectory} {
		if path != "" && !within(root, path) {
			fmt.Fprintln(rc.stdout, "Kept: "+path)
		}
	}
	fmt.Fprintf(rc.stdout, "Core still accepts this machine's credential. In Web, open the Session (Environment %s) and choose Revoke in Executor credentials, or Delete the Session. This Session's install command cannot be used again; to reconnect it, choose Rotate and install from an extracted distribution, or create a new Session.\n", config.Environment)
	return nil
}

// removeTree deletes path. keep is the running executable, which Windows
// cannot delete; it stays, and so does every directory that holds it.
func removeTree(path string, keep os.FileInfo) (kept bool, err error) {
	if keep == nil {
		return false, os.RemoveAll(path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if os.SameFile(info, keep) {
		return true, nil
	}
	if !info.IsDir() {
		return false, os.Remove(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		held, e := removeTree(filepath.Join(path, entry.Name()), keep)
		if e != nil {
			return false, e
		}
		kept = kept || held
	}
	if kept {
		return true, nil
	}
	return false, os.Remove(path)
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
