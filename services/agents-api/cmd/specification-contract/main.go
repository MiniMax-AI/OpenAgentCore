// specification-contract maintains the generated node installer projection.
package main

import (
	"flag"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
	"os"
	"strings"
)

func main() {
	write := flag.Bool("write", false, "update deploy/install/node_spec.py from the repository root")
	flag.Parse()
	projection := providers.PythonDeploymentContract()
	if !*write {
		fmt.Println(projection)
		return
	}
	path := "deploy/install/node_spec.py"
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
}
