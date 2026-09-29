package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimefs"
)

// Installation is local state, not an options-file input or an OS service.
type nativeInstallation struct {
	Version             string   `json:"version"`
	Remote              string   `json:"remote_url"`
	Environment         string   `json:"environment_id"`
	Workspace           string   `json:"workspace_directory"`
	Credential          string   `json:"credential_file"`
	CapabilityDirectory string   `json:"capability_directory"`
	ToolEnvironmentFile string   `json:"tool_environment_file,omitempty"`
	Harnesses           []string `json:"harnesses"`
}

func nativeInstallationPath() (string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "daemon", "installation.json"), nil
}

func lockNativeInstallation(root string) (*os.Root, func(), error) {
	dir := filepath.Join(root, "daemon")
	if err := runtimefs.EnsurePrivateDir(dir); err != nil {
		return nil, nil, err
	}
	held, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := runtimefs.LockDirectory(held)
	if err != nil {
		held.Close()
		return nil, nil, errors.New("installation is busy; wait for the other operation to finish")
	}
	return held, func() { unlock(); held.Close() }, nil
}

func validateNativeInstallation(c nativeInstallation) error {
	if c.Version != Version {
		return errors.New("installation version is unsupported; preserve this installation and reinstall in a separate directory")
	}
	if _, err := environmentBase(c.Remote); err != nil {
		return err
	}
	if !environmentUUID(c.Environment) {
		return errors.New("install: --environment-id requires a canonical Environment ID")
	}
	for _, p := range []string{c.Workspace, c.Credential, c.CapabilityDirectory} {
		if runtimefs.ValidateLocalPath(p) != nil {
			return errors.New("install: --workspace, --credential-file and capability destination must be clean absolute paths")
		}
	}
	info, err := os.Stat(c.Workspace)
	if err != nil || !info.IsDir() {
		return errors.New("install: --workspace requires an existing directory")
	}
	if _, _, err = executorCredential(c.Credential, c.Environment); err != nil {
		return err
	}
	if c.ToolEnvironmentFile != "" {
		var values map[string]string
		if runtimefs.ValidateLocalPath(c.ToolEnvironmentFile) != nil || readNativeJSON(c.ToolEnvironmentFile, &values) != nil {
			return errors.New("install: --tool-env-file requires an absolute JSON file containing string values")
		}
	}
	return nil
}

func runInstall(rc *runContext, args []string) error {
	o, err := parseNativeInstall(rc, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, stop := daemonize.NotifyContext(context.Background())
	defer stop()
	if err := installNativeOptions(ctx, rc, &o); err != nil {
		return err
	}
	if o.OnboardURL != "" {
		return finishOnboarding(ctx, rc, o)
	}
	return nil
}

func installNativeOptions(ctx context.Context, rc *runContext, o *nativeInstallOptions) error {
	if runtimefs.ValidateLocalPath(o.Directory) != nil || runtimefs.ValidateLocalPath(o.Bundle) != nil {
		return errors.New("install: --install-dir and --bundle-dir must be clean absolute directories")
	}
	selected, err := selectedNativeHarnesses(o.Harness)
	if err != nil {
		return err
	}
	if o.RequiredHarness != "" && !slices.Contains(selected, o.RequiredHarness) {
		return errors.New("install: --harness must include the Session Harness")
	}
	o.Version = Version
	if o.CapabilityDirectory == "" {
		o.CapabilityDirectory = filepath.Join(o.Directory, "capabilities")
	}
	bundle, err := readNativeBundle(o.Bundle, selected)
	if err != nil {
		return err
	}
	held, unlock, err := lockNativeInstallation(o.Directory)
	if err != nil {
		return err
	}
	defer unlock()
	if o.OnboardURL != "" {
		if runtimefs.ValidateLocalPath(o.Workspace) != nil {
			return errors.New("install: Session workspace is invalid on this platform")
		}
		if err = prepareOnboardingCredential(ctx, o, held); err != nil {
			return err
		}
		if err = os.MkdirAll(o.Workspace, 0700); err != nil {
			return errors.New("install: cannot create workspace with the current user's permissions; prepare it manually and retry")
		}
	}
	if err = validateNativeInstallation(o.nativeInstallation); err != nil {
		return err
	}
	var previous nativeInstallation
	raw, err := runtimefs.ReadPrivate(held, "installation.json", 1<<20)
	if err == nil {
		if decodeEnvironmentJSON(raw, &previous) != nil || previous.Version != Version || len(previous.Harnesses) == 0 {
			return errors.New("install: existing installation is unsupported; preserve it and reinstall separately")
		}
		wanted := o.nativeInstallation
		wanted.Harnesses = nil
		comparison := previous
		comparison.Harnesses = nil
		before, _ := json.Marshal(comparison)
		after, _ := json.Marshal(wanted)
		if !bytes.Equal(before, after) {
			return errors.New("install: existing connection settings differ; additive installation cannot replace them")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("install: cannot read existing installation")
	}
	all := append([]string{}, previous.Harnesses...)
	for _, name := range selected {
		if !slices.Contains(all, name) {
			all = append(all, name)
		}
	}
	slices.Sort(all)
	// Verify existing components before adding any new one; never repair or
	// upgrade an installed dependency as a side effect of adding a Harness.
	if len(previous.Harnesses) > 0 {
		if err = verifyNativeComponents(o.Directory, previous.Harnesses); err != nil {
			return err
		}
	}
	if err = installNativeBinary(o.Directory, len(previous.Harnesses) > 0); err != nil {
		return err
	}
	for _, name := range append([]string{"node"}, selected...) {
		if err = installNativeComponent(ctx, o.Bundle, o.Directory, name, bundle.Components[name]); err != nil {
			return err
		}
	}
	if err = probeNativeInstallation(ctx, o.Directory, all); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	o.Harnesses = all
	raw, _ = json.MarshalIndent(o.nativeInstallation, "", "  ")
	if err = runtimefs.WritePrivateAtomic(held, "installation.json", raw); err != nil {
		return err
	}
	fmt.Fprintln(rc.stdout, "Installation: ready; verified Harnesses:", all)
	if o.OnboardURL == "" {
		fmt.Fprintln(rc.stdout, "Daemon connection: not checked by install; run the installed oac-daemon start, then check Host connection in Core.")
	}
	fmt.Fprintln(rc.stdout, "Model configuration: not checked; configure the Session model provider in Core and send a Turn.")
	return nil
}

func verifyNativeComponents(root string, selected []string) error {
	for _, name := range append([]string{"node"}, selected...) {
		if _, ok := nativePins[name]; !ok {
			return errors.New("installation contains an unsupported Harness")
		}
		c, err := componentReceipt(nativeComponentRoot(root, name))
		if err != nil || c.Version != nativePins[name] || len(c.Files) == 0 || checkComponentFiles(nativeComponentRoot(root, name), c) != nil {
			return fmt.Errorf("installed %s is missing, modified or incompatible; reinstall separately (no automatic repair or upgrade)", name)
		}
	}
	return nil
}

func installNativeBinary(root string, existing bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "bin")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dest := filepath.Join(dir, nativeExe("oac-daemon"))
	digest := func(name string) (string, error) {
		f, e := os.Open(name)
		if e != nil {
			return "", e
		}
		defer f.Close()
		h := sha256.New()
		_, e = io.Copy(h, f)
		return hex.EncodeToString(h.Sum(nil)), e
	}
	want, err := digest(exe)
	if err != nil {
		return err
	}
	if got, e := digest(dest); e == nil {
		if got != want {
			return errors.New("install: existing daemon binary differs; in-place upgrades are unsupported")
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if existing {
		return errors.New("install: existing daemon binary is missing; preserve the installation and reinstall separately")
	}
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".oac-daemon-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(0700)
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(out.Name(), dest)
}

func runStart(rc *runContext, args []string) error {
	ctx, stop := daemonize.NotifyContext(context.Background())
	defer stop()
	flags := newFlagSet("start")
	foreground := flags.Bool("foreground", false, "stay attached to this terminal")
	if err := flags.Parse(args); err != nil {
		return errors.New("start: invalid arguments")
	}
	if flags.NArg() != 0 {
		return errors.New("start: unexpected positional arguments")
	}
	root, err := paths.Root()
	if err != nil {
		return err
	}
	held, unlock, err := lockNativeInstallation(root)
	if err != nil {
		return err
	}
	var config nativeInstallation
	raw, err := runtimefs.ReadPrivate(held, "installation.json", 1<<20)
	if err == nil {
		err = decodeEnvironmentJSON(raw, &config)
	}
	if err == nil {
		err = validateNativeInstallation(config)
	}
	if err == nil && len(config.Harnesses) == 0 {
		err = errors.New("start: no installed Harnesses; rerun install with --harness")
	}
	if err == nil {
		err = verifyNativeComponents(root, config.Harnesses)
	}
	if err == nil {
		err = probeNativeInstallation(ctx, root, config.Harnesses)
	}
	unlock()
	if err != nil {
		return fmt.Errorf("start: installation unavailable or incompatible: %w", err)
	}
	previousKinds := rc.installedKinds
	rc.installedKinds = nativeInstallationKinds(config.Harnesses)
	defer func() { rc.installedKinds = previousKinds }()
	values := nativeHarnessEnvironment(root, config.Harnesses)
	values["OAC_RUNTIME_WORKSPACE"] = config.Workspace
	values["OAC_RUNTIME_CAPABILITY_DIRECTORY"] = config.CapabilityDirectory
	values["OAC_RUNTIME_TOOL_ENV_FILE"] = config.ToolEnvironmentFile
	for key, value := range values {
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	return runEnvironmentConnect(ctx, rc, paths.DefaultProfile, !*foreground, config.Remote, config.Environment, config.Credential)
}

func useInstalledNativeHome() {
	if os.Getenv("OAC_RUNTIME_HOME") != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, e := filepath.EvalSymlinks(exe); e == nil {
		exe = resolved
	}
	if filepath.Base(filepath.Dir(exe)) != "bin" {
		return
	}
	root := filepath.Dir(filepath.Dir(exe))
	if _, err = os.Stat(filepath.Join(root, "daemon", "installation.json")); err == nil {
		_ = os.Setenv("OAC_RUNTIME_HOME", root)
	}
}
