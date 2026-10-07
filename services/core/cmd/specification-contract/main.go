// specification-contract maintains the generated deployment contract
// projections of the node installer and the TypeScript client.
package main

import (
	"flag"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"os"
	"strings"
)

func main() {
	write := flag.Bool("write", false, "update deploy/node/node_spec.py and packages/agents-client/src/deployment-contract.ts from the repository root")
	flag.Parse()
	projection, err := providers.Builtin().PythonDeploymentContract()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	typescript := sandbox.TypeScriptDeploymentContract()
	if !*write {
		fmt.Println(projection)
		fmt.Print(typescript)
		return
	}
	path := "deploy/node/node_spec.py"
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	source := string(raw)
	start := strings.Index(source, "# BEGIN GENERATED DEPLOYMENT CONTRACT")
	end := strings.Index(source, "# END GENERATED DEPLOYMENT CONTRACT")
	if start < 0 || end < start {
		panic("missing generated contract markers")
	}
	end += len("# END GENERATED DEPLOYMENT CONTRACT")
	if err = os.WriteFile(path, []byte(source[:start]+projection+source[end:]), 0644); err != nil {
		panic(err)
	}
	if err = os.WriteFile("packages/agents-client/src/deployment-contract.ts", []byte(typescript), 0644); err != nil {
		panic(err)
	}
}
