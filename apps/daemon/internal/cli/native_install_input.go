package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

type nativeInstallOptions struct {
	nativeInstallation
	Directory, Bundle, Workspace string
	OnboardURL, Authorization    string
	Interactive, NonInteractive  bool
}

func parseNativeInstall(rc *runContext, args []string) (nativeInstallOptions, error) {
	var o nativeInstallOptions
	flags := newFlagSet("install")
	flags.StringVar(&o.OnboardURL, "onboard-url", "", "Core installation endpoint")
	flags.StringVar(&o.Authorization, "authorization", "", "short-lived installation authorization (never an executor credential)")
	flags.StringVar(&o.Remote, "remote", "", "Environment remote_url from Core")
	flags.StringVar(&o.Environment, "environment-id", "", "Environment ID from Core")
	flags.StringVar(&o.Workspace, "workspace", "", "absolute workspace directory to prepare")
	flags.StringVar(&o.Credential, "credential-file", "", "absolute executor credential JSON file (never the token)")
	flags.StringVar(&o.Directory, "install-dir", "", "installation directory (default OAC_RUNTIME_HOME or ~/.oac)")
	flags.StringVar(&o.Bundle, "bundle-dir", "", "native distribution directory (defaults beside the executable)")
	flags.BoolVar(&o.Interactive, "interactive", false, "ask for missing installation options")
	flags.BoolVar(&o.NonInteractive, "non-interactive", false, "require command-line options; never prompt")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(rc.stdout)
			flags.PrintDefaults()
			return o, flag.ErrHelp
		}
		// flag errors can contain supplied values, including accidental secrets.
		return o, errors.New("install: invalid arguments; run install --help (use --credential-file for credentials)")
	}
	if flags.NArg() != 0 || (o.Interactive && o.NonInteractive) {
		return o, errors.New("install: unexpected arguments or conflicting interaction modes")
	}
	if err := prepareOnboarding(&o); err != nil {
		return o, err
	}
	if o.Directory == "" {
		var err error
		o.Directory, err = paths.Root()
		if err != nil {
			return o, err
		}
	}
	if o.Bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			return o, err
		}
		o.Bundle = filepath.Dir(exe)
	}
	if len(args) == 0 && !o.NonInteractive {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			o.Interactive = true
		}
	}
	if o.Interactive {
		input := rc.stdin
		if input == nil {
			input = os.Stdin
		}
		if err := promptNativeInstall(input, rc.stdout, &o); err != nil {
			return o, err
		}
	}
	return o, nil
}

func promptNativeInstall(input io.Reader, output io.Writer, o *nativeInstallOptions) error {
	r := bufio.NewReader(input)
	if o.OnboardURL != "" {
		return promptOnboarding(r, output, o)
	}
	// Only paths and connection identifiers are requested; tokens are read later
	// from the private credential file, never entered or echoed by this prompt.
	for _, p := range []struct {
		label    string
		value    *string
		optional bool
	}{
		{"Installation directory (Enter uses current default)", &o.Directory, true},
		{"Distribution directory (Enter uses current default)", &o.Bundle, true},
		{"Environment remote URL", &o.Remote, false},
		{"Environment ID", &o.Environment, false},
		{"Workspace directory", &o.Workspace, false},
		{"Credential JSON file path (do not enter a token)", &o.Credential, false},
	} {
		if *p.value != "" && !p.optional {
			continue
		}
		fmt.Fprint(output, p.label+": ")
		line, err := r.ReadString('\n')
		if err != nil {
			return errors.New("install: interactive input ended; use --non-interactive with complete arguments")
		}
		value := strings.TrimSpace(line)
		if value != "" {
			*p.value = value
		} else if !p.optional {
			return errors.New("install: required interactive value missing")
		}
	}
	return nil
}

func promptOnboarding(r *bufio.Reader, output io.Writer, o *nativeInstallOptions) error {
	fmt.Fprintf(output, "Environment: %s\nWorkspace: %s (set by this Session)\n", o.Environment, o.Workspace)
	for _, item := range []struct {
		label string
		value *string
	}{
		{"Installation directory", &o.Directory},
	} {
		fmt.Fprintf(output, "%s [%s]: ", item.label, *item.value)
		line, err := r.ReadString('\n')
		if err != nil {
			return errors.New("install: interactive input ended; use --non-interactive with optional --install-dir")
		}
		if value := strings.TrimSpace(line); value != "" {
			*item.value = value
		}
	}
	return nil
}
