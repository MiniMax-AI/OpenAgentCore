package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Installation is host-independent. Only the launchers select a native binary;
// Docker owns the Linux filesystem, service identities and persistent data.
type installOptions struct {
	dir, version, publicURL, host string
	port                          int
}
type installer struct {
	docker     func(context.Context, string, ...string) ([]byte, error)
	download   func(context.Context, string, string) error
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
	if err := flags.Parse(args); err != nil {
		return err
	}
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

func dockerOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w\n%s", args[0], err, output)
	}
	return output, nil
}

func downloadAsset(ctx context.Context, address, path string) error {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return errors.New("invalid release redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", address, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("release configuration exceeds size limit")
	}
	return os.WriteFile(path, raw, 0o600)
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
	if o.publicURL == "" {
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
	u, err := url.Parse(o.publicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("--public-url must be an HTTP(S) origin")
	}
	for _, value := range []string{o.publicURL, o.version} {
		if strings.ContainsAny(value, "\r\n'\\") {
			return errors.New("installation options contain invalid characters")
		}
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
		return err
	}
	return withLock(o.dir, func() error { return i.installLocked(ctx, o) })
}

func (i installer) installLocked(ctx context.Context, o installOptions) error {
	info, err := i.docker(ctx, "", "info", "--format", "{{.OSType}}/{{.Architecture}}")
	if err != nil {
		return fmt.Errorf("start Docker and check this account's access: %w", err)
	}
	switch strings.TrimSpace(string(info)) {
	case "linux/x86_64", "linux/amd64", "linux/aarch64", "linux/arm64":
	default:
		return errors.New("Docker must run Linux amd64 or arm64 containers; on Windows select Linux containers in Docker Desktop")
	}
	version, err := i.docker(ctx, "", "compose", "version", "--short")
	if err != nil {
		return err
	}
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(string(version)), "v"), "%d.%d", &major, &minor); err != nil || major < 2 || (major == 2 && minor < 26) {
		return errors.New("Docker Compose 2.26 or newer is required")
	}
	engine, err := i.docker(ctx, "", "version", "--format", "{{.Server.APIVersion}}")
	if err != nil {
		return err
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(string(engine)), "%d.%d", &major, &minor); err != nil || major < 1 || (major == 1 && minor < 45) {
		return errors.New("Docker Engine 26 or newer is required for data volume subdirectories")
	}
	stage := o.dir + ".staging"
	if err := cleanStage(stage, o.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(o.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	resume := len(entries) > 0
	working := o.dir
	if !resume {
		listener, err := net.Listen("tcp", net.JoinHostPort(o.host, strconv.Itoa(o.port)))
		if err != nil {
			return fmt.Errorf("Web port unavailable; choose --web-port: %w", err)
		}
		listener.Close()
		if err := os.Mkdir(stage, 0o700); err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		// Directory creation publishes ownership atomically, including on Windows.
		if err := os.Mkdir(filepath.Join(stage, stageMarker(o.dir)), 0o700); err != nil {
			return err
		}
		working = stage
		repository := os.Getenv("OAC_REPOSITORY")
		if repository == "" {
			repository = "MiniMax-AI/OpenAgentCore"
		}
		base := "https://github.com/" + repository + "/releases/latest/download/"
		if o.version != "latest" {
			base = "https://github.com/" + repository + "/releases/download/" + url.PathEscape(o.version) + "/"
		}
		fmt.Println("Downloading release configuration...")
		for _, name := range []string{"compose-sha256sums.txt", "compose.yaml"} {
			if err := i.download(ctx, base+name, filepath.Join(stage, name)); err != nil {
				return err
			}
		}
		contents := fmt.Sprintf("COMPOSE_PROJECT_NAME=oac-%s\nOAC_HOST='%s'\nOAC_WEB_PORT='%d'\nOAC_PUBLIC_URL='%s'\n", randomHex(5), o.host, o.port, o.publicURL)
		if err := os.WriteFile(filepath.Join(stage, ".env"), []byte(contents), 0o600); err != nil {
			return err
		}
		source, err := os.Open(i.executable)
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenFile(filepath.Join(stage, cliName()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(target, source)
		closeErr := target.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		fmt.Println("Using saved settings and retaining existing data.")
	}
	for _, name := range []string{"compose.yaml", "compose-sha256sums.txt", ".env", cliName()} {
		info, err := os.Lstat(filepath.Join(working, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("incomplete installation: preserve %s and choose another directory", o.dir)
		}
	}
	if err := verifyCompose(working); err != nil {
		return err
	}
	compose := func(args ...string) ([]byte, error) {
		return i.docker(ctx, working, append([]string{"compose"}, args...)...)
	}
	if _, err := compose("config", "--quiet"); err != nil {
		return err
	}
	images, err := compose("config", "--images")
	if err != nil {
		return err
	}
	fmt.Println("Checking and downloading images...")
	for _, image := range strings.Fields(string(images)) {
		if resume {
			if _, err := i.docker(ctx, working, "image", "inspect", image); err == nil {
				continue
			}
		}
		if _, err := i.docker(ctx, working, "pull", image); err != nil {
			return err
		}
	}
	if !resume {
		if _, err := os.Stat(o.dir); err == nil {
			if err := os.Remove(o.dir); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, o.dir); err != nil {
			return err
		}
		working = o.dir
		_ = os.Remove(filepath.Join(o.dir, stageMarker(o.dir)))
	}
	fmt.Println("Starting services...")
	if _, err := compose("up", "-d", "--wait", "--wait-timeout", "180", "--pull", "never", "--no-recreate"); err != nil {
		return fmt.Errorf("startup failed; data retained, rerun the installer: %w", err)
	}
	key, err := compose("exec", "-T", "web", "/usr/local/bin/oac-web", "core-key")
	if err != nil {
		return err
	}
	environment, err := compose("config", "--environment")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(environment), "\n") {
		if strings.HasPrefix(line, "OAC_PUBLIC_URL=") {
			o.publicURL = strings.TrimPrefix(line, "OAC_PUBLIC_URL=")
		}
	}
	fmt.Printf("\nOpenAgentCore is running.\n\nConsole: %s\nCore key: %s\nCommand: %s\n", o.publicURL, strings.TrimSpace(string(key)), filepath.Join(o.dir, cliName()))
	if u, _ := url.Parse(o.publicURL); u != nil && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
		fmt.Println("For remote access, set OAC_PUBLIC_URL in .env to a reachable origin and run oac apply.")
	}
	return nil
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

func verifyCompose(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(filepath.Join(dir, "compose-sha256sums.txt"))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if strings.TrimSpace(string(sums)) != hex.EncodeToString(digest[:])+"  compose.yaml" {
		return errors.New("Compose checksum mismatch; restore the matching release configuration")
	}
	return nil
}

func stageMarker(owner string) string {
	return fmt.Sprintf(".oac-installer-%x", sha256.Sum256([]byte(owner)))
}
