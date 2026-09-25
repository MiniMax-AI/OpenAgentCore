// Command runtime launches one user-owned packaged V1 Runtime, without a Core database.
package main

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
	"runtime"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	"github.com/moby/moby/client"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "replace-credential" {
		return replaceCredential(args[1:])
	}
	var input docker.SelfHostedLaunch
	var credential, seccomp string
	flags := flag.NewFlagSet("parsar-runtime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&input.InstallationID, "installation-id", "", "persisted local installation UUID")
	flags.StringVar(&input.EnvironmentID, "environment-id", "", "Session Environment UUID")
	flags.StringVar(&input.RemoteURL, "remote", "", "unchanged Session remote_url")
	flags.StringVar(&input.Image, "image", "", "matched Runtime image digest")
	flags.StringVar(&credential, "credential-file", "", "private restricted executor credential JSON")
	flags.StringVar(&seccomp, "seccomp-file", "", "matched Runtime seccomp file")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("invalid Runtime launch arguments")
	}
	if err := supportedHost(); err != nil {
		return err
	}
	key, err := readCredential(credential)
	if err != nil {
		return err
	}
	input.Credential = key
	raw, err := os.ReadFile(seccomp)
	if err != nil || len(raw) > 1024*1024 {
		return errors.New("cannot read Runtime seccomp profile")
	}
	input.Seccomp = string(raw)
	if err = input.Validate(); err != nil {
		return err
	}
	c, err := client.New(client.WithHost("unix:///var/run/docker.sock"))
	if err != nil {
		return errors.New("cannot open local Docker")
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name, err := docker.LaunchSelfHosted(ctx, c, input)
	if err != nil {
		// A name contains only a digest, never credentials or request diagnostics.
		if name != "" {
			fmt.Fprintln(os.Stderr, "Retained Runtime name:", name)
		}
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"container": name, "status": "started"})
}

// replaceCredential writes a rotated executor credential into the stopped
// user-owned Runtime container; its volumes and native history stay.
func replaceCredential(args []string) error {
	var name, credential string
	flags := flag.NewFlagSet("parsar-runtime replace-credential", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&name, "container", "", "user-owned Runtime container name")
	flags.StringVar(&credential, "credential-file", "", "private restricted executor credential JSON")
	if flags.Parse(args) != nil || flags.NArg() != 0 || name == "" {
		return errors.New("invalid credential replacement arguments")
	}
	if err := supportedHost(); err != nil {
		return err
	}
	key, err := readCredential(credential)
	if err != nil {
		return err
	}
	c, err := client.New(client.WithHost("unix:///var/run/docker.sock"))
	if err != nil {
		return errors.New("cannot open local Docker")
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err = docker.ReplaceSelfHostedCredential(ctx, c, name, key); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"container": name, "status": "credential_replaced"})
}

func supportedHost() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getuid() == 0 {
		return errors.New("run as a non-root user on Linux amd64 with Docker access")
	}
	return nil
}

func readCredential(path string) (docker.ExecutorCredential, error) {
	var key docker.ExecutorCredential
	raw, err := readPrivateCredential(path)
	if err != nil {
		return key, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&key) != nil || decoder.Decode(new(any)) != io.EOF {
		return key, errors.New("invalid restricted executor credential file")
	}
	return key, nil
}

func readPrivateCredential(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("absolute private credential file required")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("cannot open private credential file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, errors.New("cannot inspect private credential file")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
		return nil, errors.New("credential must be a mode-0600 owned regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return nil, errors.New("cannot read bounded credential file")
	}
	return raw, nil
}
