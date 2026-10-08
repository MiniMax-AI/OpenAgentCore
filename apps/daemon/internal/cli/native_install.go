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
	"os"
	"path/filepath"
	"runtime"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// Installation is local state, not an options-file input or an OS service.
type nativeInstallation struct {
	Version     string `json:"version"`
	Remote      string `json:"remote_url"`
	Environment string `json:"environment_id"`
	Credential  string `json:"credential_file"`
}

// UnsupportedPlatformError identifies unsupported local installation and startup.
type UnsupportedPlatformError struct{ OS, Arch string }

func (e *UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("self-hosted installation requires Linux amd64; this platform is %s/%s", e.OS, e.Arch)
}
func requireNativePlatform() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return &UnsupportedPlatformError{runtime.GOOS, runtime.GOARCH}
	}
	return nil
}

// Directory preparation belongs to installation, before credential issuance.
var prepareNativeEnvironment = prepareNativeEnvironmentDirectories

func prepareNativeEnvironmentDirectories(workspace string) error {
	for _, directory := range []string{workspace, "/environment/workspace", "/environment/initialization", "/environment/packages", "/home/runtime"} {
		if runtimefs.ValidateLocalPath(directory) != nil {
			return errors.New("install: workspace must be a clean absolute directory")
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return fmt.Errorf("install: prepare %s for the current account with write and search permissions, then retry: %v", directory, err)
		}
		file, err := os.CreateTemp(directory, ".oac-install-check-")
		if err != nil {
			return fmt.Errorf("install: prepare %s for the current account with write and search permissions, then retry: %v", directory, err)
		}
		name := file.Name()
		closeErr := file.Close()
		removeErr := os.Remove(name)
		if closeErr != nil {
			return closeErr
		}
		if removeErr != nil {
			return removeErr
		}
	}
	return nil
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
	if runtimefs.ValidateLocalPath(c.Credential) != nil {
		return errors.New("install: --credential-file must be a clean absolute path")
	}
	if _, _, err := executorCredential(c.Credential, c.Environment); err != nil {
		return err
	}

	return nil
}

func runInstall(rc *runContext, args []string) error {
	if err := requireNativePlatform(); err != nil {
		return err
	}
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
	if runtimefs.ValidateLocalPath(o.Directory) != nil || runtimefs.ValidateLocalPath(o.Bundle) != nil {
		return errors.New("install: --install-dir and --bundle-dir must be clean absolute directories")
	}
	o.Version = Version
	if _, err := readNativeBundle(o.Bundle); err != nil {
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
	if err = prepareNativeEnvironment(o.Workspace); err != nil {
		return err
	}
	if o.OnboardURL != "" {
		if err = prepareOnboardingCredential(ctx, o, held); err != nil {
			return err
		}
	}
	if err = validateNativeInstallation(o.nativeInstallation); err != nil {
		return err
	}
	var previous nativeInstallation
	raw, err := runtimefs.ReadPrivate(held, "installation.json", 1<<20)
	if err == nil {
		if decodeEnvironmentJSON(raw, &previous) != nil || previous.Version != Version {
			return errors.New("install: existing installation is unsupported; preserve it and reinstall separately")
		}
		before, _ := json.Marshal(previous)
		after, _ := json.Marshal(o.nativeInstallation)
		if !bytes.Equal(before, after) {
			return errors.New("install: existing connection settings differ; installation cannot replace them")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("install: cannot read existing installation")
	}
	if err = nativeInstallPhase(rc.stdout, "Installing sandbox launcher", func() error { return installNativeBinary(ctx, o.Bundle, o.Directory, previous.Version != "") }); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	raw, _ = json.MarshalIndent(o.nativeInstallation, "", "  ")
	if err = runtimefs.WritePrivateAtomic(held, "installation.json", raw); err != nil {
		return err
	}
	fmt.Fprintln(rc.stdout, "Installation: ready.")
	if o.OnboardURL == "" {
		fmt.Fprintln(rc.stdout, "Host connection: not checked by install; run the installed oac-daemon start, then check Host connection in Core.")
	}
	fmt.Fprintln(rc.stdout, "Model configuration: not checked; configure the Session model provider in Core and send a Turn.")
	return nil
}

// installNativeBinary installs the running oac-daemon and the distribution's
// other programs (nativeBundlePrograms) into bin.
func installNativeBinary(ctx context.Context, bundle, root string, existing bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "bin")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = installNativeProgram(ctx, exe, dir, "oac-daemon", existing); err != nil {
		return err
	}
	for _, name := range nativeBundlePrograms {
		if err = installNativeProgram(ctx, filepath.Join(bundle, name), dir, name, existing); err != nil {
			return err
		}
	}
	return nil
}

func installNativeProgram(ctx context.Context, source, dir, name string, existing bool) error {
	dest := filepath.Join(dir, name)
	digest := func(name string) (string, error) {
		f, e := os.Open(name)
		if e != nil {
			return "", e
		}
		defer f.Close()
		h := sha256.New()
		_, e = nativeCopy(ctx, h, f)
		return hex.EncodeToString(h.Sum(nil)), e
	}
	want, err := digest(source)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("install: the distribution has no %s; use the matching native distribution", name)
	}
	if err != nil {
		return err
	}
	if got, e := digest(dest); e == nil {
		if got != want {
			return fmt.Errorf("install: existing %s differs; in-place upgrades are unsupported", name)
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if existing {
		return fmt.Errorf("install: existing %s is missing; preserve the installation and reinstall separately", name)
	}
	in, err := os.Open(source)
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
	out, err := os.CreateTemp(dir, "."+name+"-")
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
	if err := requireNativePlatform(); err != nil {
		return err
	}
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
	unlock()
	if err != nil {
		return fmt.Errorf("start: installation unavailable or incompatible: %w", err)
	}
	return runSandboxLauncher(ctx, rc, !*foreground, root, config)
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
