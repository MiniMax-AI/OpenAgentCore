package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

var (
	projectName    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	installationID = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
)

// uninstallCommand deletes the installation this command belongs to. On Windows
// the running oac.exe cannot delete itself, so it stays for the user to remove.
func uninstallCommand(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("Usage: oac uninstall")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	keep := ""
	if runtime.GOOS == "windows" {
		keep = filepath.Base(exe)
	}
	dir := filepath.Dir(exe)
	project, err := installedProject(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("Nothing is installed in %s.\n", dir)
		return nil
	}
	if err != nil {
		return err
	}
	// -p names the project, so Compose needs neither the working directory nor compose.yaml.
	return uninstall(ctx, execRunner{dir: dir, project: project}, project, dir, keep, os.Stdin, os.Stdout)
}

// uninstall asks for confirmation, removes the Compose project with its data
// volume, then the installation directory (except keep) and the lock. Docker
// failures leave the directory in place so the command can be rerun.
func uninstall(ctx context.Context, runner Runner, project, dir, keep string, in io.Reader, out io.Writer) error {
	err := withLock(dir, func() error {
		// The ID lives in the data volume, so read it before the volume goes.
		id := "unavailable; find it in Settings → Service and deployment before you uninstall, or in the oac-node-<installation-id> service on a node host"
		if raw, err := runner.Output(ctx, "run", "--rm", "--no-deps", "-T", "--entrypoint", "cat", "core", "/run/oac/installation.id"); err == nil && installationID.MatchString(strings.TrimSpace(string(raw))) {
			id = strings.TrimSpace(string(raw))
		}
		images, _ := runner.Output(ctx, "config", "--images")
		fmt.Fprintf(out, "This deletes the OpenAgentCore installation in %s:\n  Compose project  %s (containers and network)\n  Data volume      %s_data (database, secrets, Core key, Session history)\n  Directory        %s\n  Installation ID  %s\n", dir, project, project, dir, id)
		fmt.Fprintf(out, "Type %q or yes to delete it: ", filepath.Base(dir))
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if answer = strings.TrimSpace(answer); answer != "yes" && answer != filepath.Base(dir) {
			return errors.New("Uninstall cancelled; nothing was changed.")
		}
		if err := runner.Run(ctx, "down", "--volumes", "--remove-orphans"); err != nil {
			return fmt.Errorf("%w; the directory was kept, so rerun oac uninstall after fixing Docker", err)
		}
		if err := removeInstallation(dir, keep); err != nil {
			return err
		}
		fmt.Fprintf(out, "\nUninstalled. Installation ID: %s\n", id)
		fmt.Fprintln(out, "Nodes on other hosts keep running, and E2B sandboxes keep running and billing until you reset the deployment. Remove each node, or run its uninstall command with the installation ID above (Uninstall in the operations guide).")
		if list := slices.Compact(slices.Sorted(slices.Values(strings.Fields(string(images))))); len(list) > 0 {
			fmt.Fprintf(out, "Images are kept so a reinstall does not download them again. To remove them: docker image rm %s\n", strings.Join(list, " "))
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The lock guards a directory that no longer exists; Windows keeps a sidecar file beside it.
	for _, path := range []string{dir + ".lock", filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".lock.runtime.lock")} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	if keep != "" {
		fmt.Fprintf(out, "\nWindows cannot delete the running %s. Remove the rest with:\n  Remove-Item -Recurse -Force \"%s\"\n", keep, dir)
	}
	return nil
}

// installedProject reads the Compose project from .env; a missing .env means nothing is installed.
func installedProject(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "COMPOSE_PROJECT_NAME="); ok {
			if value = strings.Trim(strings.TrimSpace(value), `'"`); projectName.MatchString(value) {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("%s has no valid COMPOSE_PROJECT_NAME, so it is not an installation; nothing was changed", filepath.Join(dir, ".env"))
}

// removeInstallation deletes dir, or everything in it except keep.
func removeInstallation(dir, keep string) error {
	if keep == "" {
		return os.RemoveAll(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), keep) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
