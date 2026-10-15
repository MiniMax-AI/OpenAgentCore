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
	targets := []string{root}
	if cache := downloadCache(root); cache != "" && !within(root, cache) {
		targets = append(targets, cache)
	}
	fmt.Fprintln(rc.stdout, "This stops the daemon and deletes, with everything inside:")
	for _, target := range targets {
		fmt.Fprintln(rc.stdout, "  "+target)
	}
	fmt.Fprintln(rc.stdout, "The installation directory holds the executor credential, native history and Harnesses; native-download holds the download lock. The workspace is kept.")
	fmt.Fprint(rc.stdout, "Continue? [y/N] ")
	answer, _ := bufio.NewReader(rc.stdin).ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return errors.New("uninstall: cancelled; nothing was deleted")
	}
	if err = runStop(rc, nil); err != nil {
		return err
	}
	// Windows cannot delete the executable it is running.
	running := ""
	if runtime.GOOS == "windows" {
		running, _ = os.Executable()
	}
	for _, target := range targets[1:] {
		if err = os.RemoveAll(target); err != nil {
			return fmt.Errorf("uninstall: cannot delete %s: %w", target, err)
		}
	}
	remaining, err := removeInstallation(root, running)
	if err != nil {
		return fmt.Errorf("uninstall: cannot delete %s; close programs using it and run the command again: %w", root, err)
	}
	if remaining {
		// Windows keeps the running executable open until this process exits.
		deleteAfterExit(root)
	}
	fmt.Fprintln(rc.stdout, "oac-daemon: uninstalled")
	if config.Workspace != "" {
		fmt.Fprintln(rc.stdout, "Workspace kept: "+config.Workspace)
	}
	fmt.Fprintf(rc.stdout, "Core still accepts this machine's credential. In Web, open the Session (Environment %s) and choose Revoke in Executor credentials, or Delete the Session.\n", config.Environment)
	return nil
}

// removeInstallation deletes dir. A running executable cannot be deleted on
// Windows, so everything else goes and the function reports what is left.
func removeInstallation(dir, running string) (remaining bool, err error) {
	if running == "" || !within(dir, running) {
		return false, os.RemoveAll(dir)
	}
	return true, removeAllExcept(dir, running)
}

func removeAllExcept(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		switch {
		case path == keep:
		case entry.IsDir() && within(path, keep):
			if err = removeAllExcept(path, keep); err != nil {
				return err
			}
		default:
			if err = os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// downloadCache is the bootstrap's native-download directory (staging and the
// download lock). It sits in the Runtime home, which the default installation
// directory <home>/environments/<id> shares with other installations.
func downloadCache(root string) string {
	home := root
	if filepath.Base(filepath.Dir(root)) == "environments" {
		home = filepath.Dir(filepath.Dir(root))
	}
	cache := filepath.Join(home, "native-download")
	if info, err := os.Lstat(cache); err != nil || !info.IsDir() {
		return ""
	}
	return cache
}
