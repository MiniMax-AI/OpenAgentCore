// Package cli is the oac-daemon subcommand router. Stdlib-only flag
// dispatch — no cobra — so the produced binary stays small.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Version is the daemon's reported version. The Makefile overrides
// this via -ldflags at build time.
var Version = "0.0.0-dev"

type command struct {
	name    string
	summary string
	run     func(ctx *runContext, args []string) error
}

// runContext carries command I/O and an optional installed Harness selection.
// Tests inject streams; production uses the OS streams.
type runContext struct {
	installedKinds map[string]bool
	stdin          io.Reader
	stdout         io.Writer
	stderr         io.Writer
}

func defaultRunContext() *runContext {
	return &runContext{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
}

// commands lists subcommands in --help render order: the user's
// likely flow connect → status → stop / logs → logout.
var commands = []command{
	{name: "install", summary: "Install a native daemon and selected Harnesses", run: runInstall},
	{name: "start", summary: "Start the installed native daemon", run: runStart},
	{name: "resume", summary: "Wake one planned hosted suspension", run: runResume},
	{name: "runtime-mcp-exec", summary: "Execute an installed MCP server", run: runRuntimeMCP},
	{name: "placement", summary: "Enroll or retire an explicitly managed local execution placement", run: runPlacement},
	{name: "connect", summary: "Pair, open the reverse WebSocket, and start serving prompts", run: runConnect},
	{name: "status", summary: "Print the paired profile and daemon state", run: runStatus},
	{name: "stop", summary: "Stop a background `connect -b` daemon", run: runStop},
	{name: "logs", summary: "Tail the background daemon's log file", run: runLogs},
	{name: "logout", summary: "Forget the credential for a profile", run: runLogout},
	{name: "version", summary: "Print the daemon version and exit", run: runVersion},
}

// Execute is main.go's entry point with os.Args[1:].
func Execute(argv []string) error {
	return execute(defaultRunContext(), argv)
}

func execute(ctx *runContext, argv []string) error {
	useInstalledNativeHome()
	if err := validateRuntimeConfiguration(); err != nil {
		return err
	}
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help" {
		printRootHelp(ctx.stdout)
		if len(argv) == 0 {
			return fmt.Errorf("missing subcommand")
		}
		return nil
	}
	name := argv[0]
	for _, c := range commands {
		if c.name == name {
			return c.run(ctx, argv[1:])
		}
	}
	printRootHelp(ctx.stderr)
	return fmt.Errorf("unknown subcommand %q", name)
}

func printRootHelp(w io.Writer) {
	fmt.Fprintln(w, "oac-daemon — OpenAgentCore reverse-WebSocket agent daemon")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: oac-daemon <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `oac-daemon <subcommand> --help` for subcommand-specific flags.")
}

// newFlagSet returns a FlagSet that doesn't print its own usage to
// stderr on error — we surface the error via Execute's return value
// so stderr noise stays predictable for callers piping oac-daemon.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func runVersion(ctx *runContext, _ []string) error {
	fmt.Fprintln(ctx.stdout, Version)
	return nil
}
