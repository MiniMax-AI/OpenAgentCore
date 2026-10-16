package main

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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/publicorigin"
)

// Installation is host-independent. Only the launchers select a native binary;
// Docker owns the Linux filesystem, service identities and persistent data.
type installOptions struct {
	dir, version, publicURL, host string
	port                          int
	upgrade                       bool
	// given names the settings flags the caller passed, which an existing installation ignores.
	given []string
}
type installer struct {
	docker     func(context.Context, string, ...string) ([]byte, error)
	download   func(context.Context, string) ([]byte, error)
	executable string
}

func installCommand(ctx context.Context, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("oac install", flag.ContinueOnError)
	var o installOptions
	flags.StringVar(&o.dir, "install-dir", filepath.Join(home, ".oac", "core"), "absolute installation directory")
	flags.StringVar(&o.version, "version", "latest", "release tag")
	flags.StringVar(&o.publicURL, "public-url", "", "URL reachable by browsers and nodes")
	flags.StringVar(&o.host, "host", "0.0.0.0", "Web bind address")
	flags.IntVar(&o.port, "web-port", 8080, "Web port")
	flags.BoolVar(&o.upgrade, "upgrade", false, "upgrade an existing installation to this release")
	// The flag package's own report would repeat the error main prints.
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stderr)
			flags.PrintDefaults()
		}
		return err
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "host" || f.Name == "web-port" || f.Name == "public-url" {
			o.given = append(o.given, f.Name)
		}
	})
	if flags.NArg() != 0 {
		return errors.New("unexpected installation arguments")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	i := installer{docker: dockerOutput, download: downloadAsset, executable: exe}
	return i.install(ctx, o)
}

// dockerOutput returns standard output. A failure carries Docker's own reason, which it writes to standard error.
func dockerOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = dockerEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var output []byte
	var err error
	if len(args) > 0 && args[0] == "pull" {
		// A pull transfers hundreds of megabytes; Docker's own progress shows it is not stuck.
		cmd.Stdout = os.Stdout
		err = cmd.Run()
	} else {
		output, err = cmd.Output()
	}
	if err != nil {
		if reason := strings.TrimSpace(stderr.String()); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, err
	}
	return output, nil
}

// downloadRetryDelay spaces download attempts; tests shorten it.
var downloadRetryDelay = time.Second

// downloadAsset retries network errors, 429 and 5xx responses; each attempt has its own timeout.
func downloadAsset(ctx context.Context, address string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return errors.New("invalid release redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		raw, status, err := fetch(client, req)
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case err == nil && status == http.StatusOK:
			if len(raw) > 1<<20 {
				return nil, errors.New("release configuration exceeds size limit")
			}
			return raw, nil
		case err == nil:
			err = fmt.Errorf("HTTP %d", status)
		}
		// Status 0 is a network error.
		if attempt == 3 || (status != 0 && status != http.StatusTooManyRequests && status < 500) {
			return nil, fmt.Errorf("download %s: %w; check the network or HTTPS_PROXY and rerun", path.Base(req.URL.Path), err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * downloadRetryDelay):
		}
	}
}

// fetch returns the response status and body, or status 0 with a network error.
func fetch(client *http.Client, req *http.Request) ([]byte, int, error) {
	res, err := client.Do(req)
	if err != nil {
		// The cause without the request line, which repeats the whole URL.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return nil, 0, err
	}
	return raw, res.StatusCode, nil
}

func (i installer) install(ctx context.Context, o installOptions) error {
	if !filepath.IsAbs(o.dir) || filepath.Clean(o.dir) == filepath.VolumeName(o.dir)+string(filepath.Separator) {
		return errors.New("--install-dir must name an absolute directory other than the filesystem root")
	}
	if o.port < 1 || o.port > 65535 {
		return errors.New("--web-port must be between 1 and 65535")
	}
	if net.ParseIP(o.host) == nil {
		return errors.New("--host must be an IP address")
	}
	detected := o.publicURL == ""
	if detected {
		o.publicURL = "http://localhost:" + strconv.Itoa(o.port)
		if o.host == "0.0.0.0" {
			// UDP connect selects a route without transmitting a packet.
			if route, err := net.DialTimeout("udp4", "1.1.1.1:53", time.Second); err == nil {
				address := route.LocalAddr().(*net.UDPAddr).IP
				route.Close()
				if address.IsPrivate() {
					o.publicURL = "http://" + net.JoinHostPort(address.String(), strconv.Itoa(o.port))
				}
			}
		}
	}
	if _, err := publicorigin.New(o.publicURL); err != nil {
		return errors.New("--public-url must be a canonical http or https origin without a path, such as https://core.example")
	}
	if strings.ContainsAny(o.version, "\r\n'\\") {
		return errors.New("installation options contain invalid characters")
	}
	o.dir = filepath.Clean(o.dir)
	if err := os.MkdirAll(filepath.Dir(o.dir), 0o700); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(o.dir))
	if err != nil {
		return err
	}
	o.dir = filepath.Join(parent, filepath.Base(o.dir))
	if info, err := os.Lstat(o.dir); err == nil && !info.IsDir() {
		return errors.New("installation path must be a directory, not a symbolic link or file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return accessError(o.dir, err)
	}
	return withLock(o.dir, func() error { return i.installLocked(ctx, o, detected) })
}

// installLocked installs or resumes under the lock; detected says the console origin was not given.
func (i installer) installLocked(ctx context.Context, o installOptions, detected bool) error {
	info, err := i.docker(ctx, "", "info", "--format", "{{.OSType}}/{{.Architecture}}")
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return errors.New("Docker is not installed; install Docker Engine 26 or newer with the Compose plugin, or Docker Desktop on macOS and Windows, then rerun the installer")
	case err != nil && strings.Contains(err.Error(), "permission denied while trying to connect to"):
		return errors.New("This account cannot use Docker: add it to the docker group and log in again, or use Docker Desktop or rootless Docker. Do not run the installer with sudo.")
	case err != nil:
		return fmt.Errorf("Docker is not running or not reachable; start it and rerun the installer: %w", err)
	}
	switch strings.TrimSpace(string(info)) {
	case "linux/x86_64", "linux/amd64", "linux/aarch64", "linux/arm64":
	default:
		return errors.New("Docker must run Linux amd64 or arm64 containers; on Windows select Linux containers in Docker Desktop")
	}
	var major, minor int
	version, err := i.docker(ctx, "", "compose", "version", "--short")
	if err == nil {
		_, err = fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(string(version)), "v"), "%d.%d", &major, &minor)
	}
	if err != nil || major < 2 || (major == 2 && minor < 26) {
		return errors.New("Docker Compose 2.26 or newer is required; install or update the Docker Compose plugin and rerun the installer")
	}
	engine, err := i.docker(ctx, "", "version", "--format", "{{.Server.APIVersion}}")
	if err != nil {
		return err
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(string(engine)), "%d.%d", &major, &minor); err != nil || major < 1 || (major == 1 && minor < 45) {
		return errors.New("Docker Engine 26 or newer is required for data volume subdirectories; update Docker and rerun the installer")
	}
	stage := o.dir + ".staging"
	if err := cleanStage(stage, o.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(o.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fresh := len(entries) == 0
	if fresh && o.upgrade {
		return fmt.Errorf("No installation in %s; pass the installation's --install-dir.", o.dir)
	}
	installed := buildRevision
	if !fresh {
		for _, name := range []string{"compose.yaml", ".env", cliName()} {
			info, err := os.Lstat(filepath.Join(o.dir, name))
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("incomplete installation: preserve %s and choose another directory", o.dir)
			}
		}
		raw, err := os.ReadFile(filepath.Join(o.dir, "compose.yaml"))
		if err != nil {
			return err
		}
		if installed = composeRevision(raw); installed == "" {
			return fmt.Errorf("incomplete installation: preserve %s and choose another directory", o.dir)
		}
	}
	upgrade := installed != buildRevision
	if upgrade && !o.upgrade {
		return fmt.Errorf("This installation runs release %s; this installer is %s. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade.", short(installed), short(buildRevision))
	}
	compose := installCompose{docker: i.docker, dir: o.dir}
	cleanedOverride := false
	if fresh || upgrade {
		if fresh {
			listener, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.port)))
			if err != nil {
				return fmt.Errorf("Web port unavailable; choose --web-port: %w%s", err, i.portHolderHint(ctx, o.port))
			}
			listener.Close()
		}
		if err := os.Mkdir(stage, 0o700); err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		// Directory creation publishes ownership atomically, including on Windows.
		if err := os.Mkdir(filepath.Join(stage, stageMarker(o.dir)), 0o700); err != nil {
			return err
		}
		release, err := i.releaseCompose(ctx, o.version)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "compose.yaml"), release, 0o600); err != nil {
			return err
		}
		if fresh {
			contents := fmt.Sprintf("COMPOSE_PROJECT_NAME=oac-%s\nOAC_HOST='%s'\nOAC_WEB_PORT='%d'\nOAC_PUBLIC_URL='%s'\n", randomHex(5), o.host, o.port, o.publicURL)
			if err := os.WriteFile(filepath.Join(stage, ".env"), []byte(contents), 0o600); err != nil {
				return err
			}
			compose.dir = stage
		} else {
			// The staged release runs with the installation's settings, project name and override.
			compose.files = []string{"-f", filepath.Join(stage, "compose.yaml")}
			override := filepath.Join(o.dir, "compose.override.yaml")
			if raw, err := os.ReadFile(override); err == nil {
				if cleaned, changed := dropPayloadBind(raw); changed {
					// The release serves the node payload from the data volume, so the offline installer's host bind goes.
					if err := os.WriteFile(filepath.Join(stage, "compose.override.yaml"), cleaned, 0o600); err != nil {
						return err
					}
					override = filepath.Join(stage, "compose.override.yaml")
					cleanedOverride = true
				}
				compose.files = append(compose.files, "-f", override)
			}
			compose.files = append(compose.files, "--project-directory", o.dir)
		}
		if err := copyExecutable(i.executable, filepath.Join(stage, cliName())); err != nil {
			return err
		}
	} else {
		fmt.Println("Using saved settings and retaining existing data.")
	}
	if err := compose.Run(ctx, "config", "--quiet"); err != nil {
		return err
	}
	images, err := compose.Output(ctx, "config", "--images")
	if err != nil {
		return err
	}
	fmt.Println("Checking images...")
	for _, image := range strings.Fields(string(images)) {
		if _, err := i.docker(ctx, "", "image", "inspect", image); err == nil {
			continue
		}
		fmt.Printf("Pulling %s…\n", image)
		if _, err := i.docker(ctx, "", "pull", image); err != nil {
			return fmt.Errorf("Docker could not pull %s: %w\nImage pulls go through the Docker daemon, which does not use this shell's proxy settings: if the registry is unreachable, configure the daemon's proxy or a registry mirror, or install from the offline archive (Offline hosts in the installation options).", image, err)
		}
	}
	if upgrade {
		// Init upgrades the data volume or refuses; until then the running release is untouched.
		fmt.Println("Checking the upgrade...")
		for _, check := range [][]string{checkConfig, initVolume} {
			if err := compose.Run(ctx, check...); err != nil {
				return fmt.Errorf("Upgrade stopped; the running services are unchanged: %w", err)
			}
		}
		names := []string{cliName(), "compose.yaml"}
		if cleanedOverride {
			names = append(names, "compose.override.yaml")
		}
		for _, name := range names {
			if err := os.Rename(filepath.Join(stage, name), filepath.Join(o.dir, name)); err != nil {
				return err
			}
		}
		if cleanedOverride {
			// Nothing serves the offline installer's host copy any more.
			_ = os.RemoveAll(filepath.Join(o.dir, "node-payload"))
		}
	}
	if !fresh {
		// Installations from earlier releases kept a checksum that no longer describes compose.yaml.
		_ = os.Remove(filepath.Join(o.dir, "compose-sha256sums.txt"))
	}
	if fresh {
		if _, err := os.Stat(o.dir); err == nil {
			if err := os.Remove(o.dir); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, o.dir); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(o.dir, stageMarker(o.dir)))
	}
	compose = installCompose{docker: i.docker, dir: o.dir}
	if !fresh {
		if differing := i.differingSettings(o); len(differing) > 0 {
			fmt.Printf("Ignored %s: they differ from the saved settings. To change them, edit .env and run %s apply.\n", strings.Join(differing, ", "), filepath.Join(o.dir, cliName()))
		}
	}
	fmt.Println("Starting services (up to 3 minutes)…")
	if err := apply(ctx, compose, o.dir, "rerun the installer"); err != nil {
		return err
	}
	origin, err := consoleOrigin(ctx, compose)
	if err != nil {
		return err
	}
	command := filepath.Join(o.dir, cliName())
	fmt.Printf("\nOpenAgentCore is running.\n\nConsole: %s\n", origin)
	if fresh {
		key, err := compose.Output(ctx, "exec", "-T", "web", "/usr/local/bin/oac-web", "core-key")
		if err != nil {
			return err
		}
		fmt.Printf("Core key: %s\n", strings.TrimSpace(string(key)))
	} else {
		fmt.Printf("Core key: %s core-key --show\n", command)
	}
	fmt.Printf("Command: %s\n", command)
	if upgrade {
		fmt.Printf("Upgraded from %s to %s. Settings → Sandbox and scheduling offers this release's Runtime; sandboxes keep their current Runtime until you apply it.\n", short(installed), short(buildRevision))
	}
	if fresh && detected {
		fmt.Printf("Browsers must open exactly %s. If they reach this host at another address, set OAC_PUBLIC_URL in .env and run %s apply.\n", origin, command)
	}
	return nil
}

// dropPayloadBind removes the offline installer's Web bind of /node-payload from a JSON override and keeps every other entry. Other formats are the operator's and stay as they are.
func dropPayloadBind(raw []byte) ([]byte, bool) {
	var override map[string]any
	if json.Unmarshal(raw, &override) != nil {
		return nil, false
	}
	services, _ := override["services"].(map[string]any)
	web, _ := services["web"].(map[string]any)
	volumes, _ := web["volumes"].([]any)
	kept := make([]any, 0, len(volumes))
	for _, volume := range volumes {
		if entry, ok := volume.(map[string]any); !ok || entry["target"] != "/node-payload" {
			kept = append(kept, volume)
		}
	}
	if len(kept) == len(volumes) {
		return nil, false
	}
	web["volumes"] = kept
	cleaned, err := json.MarshalIndent(override, "", "  ")
	return append(cleaned, '\n'), err == nil
}

// portHolderHint names an OpenAgentCore Compose project that publishes the port. A project whose directory is gone keeps running; one whose directory exists is a live installation, so the hint names it instead of offering to delete it.
func (i installer) portHolderHint(ctx context.Context, port int) string {
	rows, err := i.docker(ctx, "", "ps", "--filter", "publish="+strconv.Itoa(port), "--format", `{{.Label "com.docker.compose.project"}}	{{.Label "com.docker.compose.project.working_dir"}}`)
	if err != nil {
		return ""
	}
	for _, row := range strings.Split(string(rows), "\n") {
		name, dir, _ := strings.Cut(strings.TrimSpace(row), "\t")
		if !strings.HasPrefix(name, "oac-") {
			continue
		}
		if dir == "" {
			return fmt.Sprintf("\nThe Compose project %s holds it.", name)
		}
		if _, err := os.Stat(dir); err == nil {
			return fmt.Sprintf("\nThe installation in %s holds it; upgrade or manage it with --install-dir %s.", dir, dir)
		}
		return fmt.Sprintf("\nThe Compose project %s holds it, and its installation directory %s no longer exists. Remove it with: docker compose -p %s down --volumes --remove-orphans (this deletes its data)", name, dir, name)
	}
	return ""
}

// consoleOrigin is the origin Web accepts, with the installation's defaults applied.
func consoleOrigin(ctx context.Context, compose Runner) (string, error) {
	raw, err := compose.Output(ctx, "config", "--format", "json")
	if err != nil {
		return "", err
	}
	var config struct {
		Services struct {
			Web struct{ Environment map[string]string }
		}
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", err
	}
	return config.Services.Web.Environment["OAC_PUBLIC_URL"], nil
}

// differingSettings names the settings flags the caller passed that disagree with the saved .env.
func (i installer) differingSettings(o installOptions) []string {
	saved := map[string]string{}
	raw, _ := os.ReadFile(filepath.Join(o.dir, ".env"))
	for _, line := range strings.Split(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			saved[name] = strings.Trim(value, "'")
		}
	}
	want := map[string]string{"host": saved["OAC_HOST"], "web-port": saved["OAC_WEB_PORT"], "public-url": saved["OAC_PUBLIC_URL"]}
	given := map[string]string{"host": o.host, "web-port": strconv.Itoa(o.port), "public-url": o.publicURL}
	var names []string
	for _, name := range o.given {
		if given[name] != want[name] {
			names = append(names, "--"+name)
		}
	}
	return names
}

// releaseCompose downloads the requested release's compose.yaml, verifies its published checksum and requires it to start this installer's release.
func (i installer) releaseCompose(ctx context.Context, version string) ([]byte, error) {
	base := "https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/"
	if version != "latest" {
		base = "https://github.com/MiniMax-AI/OpenAgentCore/releases/download/" + url.PathEscape(version) + "/"
	}
	fmt.Println("Downloading release configuration...")
	sums, err := i.download(ctx, base+"compose-sha256sums.txt")
	if err != nil {
		return nil, err
	}
	compose, err := i.download(ctx, base+"compose.yaml")
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(compose)
	if strings.TrimSpace(string(sums)) != hex.EncodeToString(digest[:])+"  compose.yaml" {
		return nil, errors.New("Compose checksum mismatch; rerun the installer")
	}
	// A moving latest release can differ from the release that published this installer.
	if revision := composeRevision(compose); revision != buildRevision {
		return nil, fmt.Errorf("release configuration %s does not match this installer %s; rerun the installer", short(revision), short(buildRevision))
	}
	return compose, nil
}

// composeRevision returns the release a Compose file starts, from init's OAC_REVISION.
func composeRevision(compose []byte) string {
	for _, line := range strings.Split(string(compose), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "OAC_REVISION:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func copyExecutable(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// installCompose runs Docker Compose through the installer's Docker command, so installation shares oac apply's start.
type installCompose struct {
	docker func(context.Context, string, ...string) ([]byte, error)
	dir    string
	files  []string
}

// Output reports a failure as the last line of Compose's output, which holds the cause; the progress lines before it only repeat what happened.
func (c installCompose) Output(ctx context.Context, args ...string) ([]byte, error) {
	output, err := c.docker(ctx, c.dir, append(append([]string{"compose"}, c.files...), args...)...)
	if err != nil {
		if lines := strings.Split(strings.TrimSpace(err.Error()), "\n"); len(lines) > 1 {
			err = errors.New(strings.TrimSpace(lines[len(lines)-1]))
		}
	}
	return output, err
}

func (c installCompose) Run(ctx context.Context, args ...string) error {
	_, err := c.Output(ctx, args...)
	return err
}

func cliName() string {
	if runtime.GOOS == "windows" {
		return "oac.exe"
	}
	return "oac"
}

func cleanStage(stage, owner string) error {
	info, err := os.Lstat(stage)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("staging path must be a directory, not a link or file")
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		marker := filepath.Join(stage, stageMarker(owner))
		info, err := os.Lstat(marker)
		if err != nil || !info.IsDir() {
			return errors.New("unrecognized staging directory; preserve it and choose another installation directory")
		}
		contents, err := os.ReadDir(marker)
		if err != nil || len(contents) != 0 {
			return errors.New("invalid staging ownership marker")
		}

	}
	return os.RemoveAll(stage)
}

func stageMarker(owner string) string {
	return fmt.Sprintf(".oac-installer-%x", sha256.Sum256([]byte(owner)))
}
