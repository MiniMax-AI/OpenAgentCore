package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
)

// This packaged helper is invoked by the existing Core initializer. It has no
// daemon connection, credentials, scheduling or retry behavior.
func runRuntimeCapabilities(ctx *runContext, args []string) error {
	if len(args) != 0 {
		return agentcapabilities.ErrInvalid
	}
	var request agentcapabilities.Operation
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 32<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Version != 1 {
		return agentcapabilities.ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return agentcapabilities.ErrInvalid
	}
	root, err := os.OpenRoot("/environment")
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	defer root.Close()
	installed, err := root.OpenRoot("initialization/capabilities")
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	defer installed.Close()
	switch request.Action {
	case "plugin":
		err = agentcapabilities.InstallPlugin(installed, request.Slot, request.Archive, request.Plugin)
	case "finalize":
		workspace, openErr := root.OpenRoot("workspace")
		if openErr != nil {
			return agentcapabilities.ErrInvalid
		}
		defer workspace.Close()
		err = agentcapabilities.Finalize(workspace, installed, request.Sources)
	default:
		err = agentcapabilities.ErrInvalid
	}
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	_, err = fmt.Fprintln(ctx.stdout, `{"version":1,"outcome":"completed"}`)
	return err
}
