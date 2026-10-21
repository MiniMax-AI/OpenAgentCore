package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// Installation is local state, not an options-file input or an OS service.
type nativeInstallation struct {
	Version             string   `json:"version"`
	Remote              string   `json:"remote_url"`
	Environment         string   `json:"environment_id"`
	Workspace           string   `json:"workspace_directory"`
	Credential          string   `json:"credential_file"`
	CapabilityDirectory string   `json:"capability_directory"`
	ComponentDirectory  string   `json:"component_directory"`
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

// validateNativeComponentDirectory accepts only a version directory of a components store.
func validateNativeComponentDirectory(dir string) error {
	if runtimefs.ValidateLocalPath(dir) != nil || filepath.Base(dir) != Version || filepath.Base(filepath.Dir(dir)) != "components" {
		return errors.New("invalid component directory")
	}
	return nil
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
	if validateNativeComponentDirectory(c.ComponentDirectory) != nil {
		return errors.New("installation component directory is invalid; reinstall in a separate directory")
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
		return nativeInstallError(err)
	}
	if o.OnboardURL != "" {
		return finishOnboarding(ctx, rc, o)
	}
	return nil
}

func installNativeOptions(ctx context.Context, rc *runContext, o *nativeInstallOptions) error {
	if o.Directory == "" {
		return errors.New("install: --environment-id is required; it names the default installation directory")
	}
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
	if _, err = os.Stat(filepath.Join(o.Directory, "daemon", "installation.json")); err != nil {
		foreign, e := foreignEntries(o.Directory)
		if e != nil {
			return errors.New("install: cannot read --install-dir " + o.Directory)
		}
		if len(foreign) > 0 {
			return fmt.Errorf("install: %s is not empty and holds no installation (it contains %s); choose an empty --install-dir", o.Directory, strings.Join(foreign, ", "))
		}
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
	if err = cleanNativeTemporaryFiles(o.Directory); err != nil {
		return err
	}
	// A rerun keeps the shared components its installation already uses.
	var previous nativeInstallation
	raw, err := runtimefs.ReadPrivate(held, "installation.json", 1<<20)
	if err == nil {
		if decodeEnvironmentJSON(raw, &previous) != nil || previous.Version != Version || len(previous.Harnesses) == 0 {
			return errors.New("install: existing installation is unsupported; preserve it and reinstall separately")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("install: cannot read existing installation")
	}
	if len(previous.Harnesses) > 0 {
		if validateNativeComponentDirectory(previous.ComponentDirectory) != nil {
			return errors.New("install: existing installation is unsupported; preserve it and reinstall separately")
		}
		o.ComponentDirectory = previous.ComponentDirectory
	} else if o.ComponentDirectory, err = nativeStoreDirectory(); err != nil {
		return err
	}
	// An installation that did not publish its record must not pin the shared components. Removing
	// the entry only lowers the count, so it needs no store lock; a later install or uninstall collects.
	defer func() {
		if _, err := os.Stat(filepath.Join(o.Directory, "daemon", "installation.json")); errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(nativeUserEntry(o.ComponentDirectory, o.Directory))
		}
	}()
	// Fetch before claiming the credential, so a failed download leaves the Session's credential unclaimed.
	if err = ensureNativeComponents(ctx, rc.stdout, o, bundle, selected); err != nil {
		return err
	}
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
	if len(previous.Harnesses) > 0 {
		wanted := o.nativeInstallation
		wanted.Harnesses = nil
		comparison := previous
		comparison.Harnesses = nil
		before, _ := json.Marshal(comparison)
		after, _ := json.Marshal(wanted)
		if !bytes.Equal(before, after) {
			return errors.New("install: existing connection settings differ; additive installation cannot replace them")
		}
	}
	all := append([]string{}, previous.Harnesses...)
	for _, name := range selected {
		if !slices.Contains(all, name) {
			all = append(all, name)
		}
	}
	slices.Sort(all)
	// Harnesses installed earlier but not selected now were not touched above; they are verified, never repaired.
	if untouched := slices.DeleteFunc(slices.Clone(previous.Harnesses), func(name string) bool { return slices.Contains(selected, name) }); len(untouched) > 0 {
		if err = nativeInstallPhase(rc.stdout, "Verifying installed components", func() error { return verifyNativeComponents(ctx, o.ComponentDirectory, untouched) }); err != nil {
			return err
		}
	}
	if err = nativeInstallPhase(rc.stdout, "Installing Runtime", func() error { return installNativeBinary(ctx, o.Directory, len(previous.Harnesses) > 0) }); err != nil {
		return err
	}
	if err = nativeInstallPhase(rc.stdout, "Checking installed programs", func() error { return probeNativeInstallation(ctx, o.ComponentDirectory, all) }); err != nil {
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

// ensureNativeComponents registers the installation as a user of this version's shared components,
// collects what no installation uses, and installs the missing components. It holds the store lock.
func ensureNativeComponents(ctx context.Context, out io.Writer, o *nativeInstallOptions, bundle nativeBundle, selected []string) error {
	unlock, err := lockNativeStore(ctx, out, filepath.Dir(o.ComponentDirectory))
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	defer unlock()
	if err = requireNativeDirectory(o.ComponentDirectory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("install: %w", err)
	}
	if err = addNativeUser(o.ComponentDirectory, o.Directory); err != nil {
		return err
	}
	_ = collectNativeStore(filepath.Dir(o.ComponentDirectory))
	if err = cleanNativeStaging(o.ComponentDirectory); err != nil {
		return err
	}
	for _, name := range append([]string{"node"}, selected...) {
		if err = ensureNativeComponent(ctx, out, o, name, bundle.Components[name]); err != nil {
			return err
		}
	}
	return nil
}

// verifyNativeComponents checks node and the named Harnesses in the shared components directory.
func verifyNativeComponents(ctx context.Context, components string, harnesses []string) error {
	for _, name := range append([]string{"node"}, harnesses...) {
		if _, ok := nativePins[name]; !ok {
			return errors.New("installation contains an unsupported Harness")
		}
		if err := verifyNativeComponent(ctx, filepath.Join(components, name), name, nil); err != nil {
			return err
		}
	}
	return nil
}

func installNativeBinary(ctx context.Context, root string, existing bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "bin")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dest := filepath.Join(dir, nativeExe("oac-daemon"))
	want, err := nativeFileDigest(ctx, exe)
	if err != nil {
		return err
	}
	if got, e := nativeFileDigest(ctx, dest); e == nil {
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
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err = requireNativeSpace(dir, uint64(info.Size())); err != nil {
		return err
	}
	out, err := os.CreateTemp(dir, ".oac-daemon-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = nativeCopy(ctx, out, in)
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
		err = verifyNativeComponents(ctx, config.ComponentDirectory, config.Harnesses)
	}
	if err == nil {
		err = probeNativeInstallation(ctx, config.ComponentDirectory, config.Harnesses)
	}
	unlock()
	if err != nil {
		return fmt.Errorf("start: installation unavailable or incompatible: %w", err)
	}
	previousKinds := rc.installedKinds
	rc.installedKinds = nativeInstallationKinds(config.Harnesses)
	defer func() { rc.installedKinds = previousKinds }()
	values := nativeHarnessEnvironment(config.ComponentDirectory, config.Harnesses)
	values["OAC_RUNTIME_WORKSPACE"] = config.Workspace
	values["OAC_RUNTIME_CAPABILITY_DIRECTORY"] = config.CapabilityDirectory
	values["OAC_RUNTIME_TOOL_ENV_FILE"] = config.ToolEnvironmentFile
	for key, value := range values {
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	return runEnvironmentConnect(ctx, rc, paths.DefaultProfile, !*foreground, true, config.Remote, config.Environment, config.Credential)
}

func useInstalledNativeHome() {
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
