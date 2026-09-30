package node

// Transport operation names map to the authored Provider method declarations.
var operationMethods = map[string]string{
	"create":          "Create",
	"info":            "GetInfo",
	"renew":           "Renew",
	"kill":            "Kill",
	"command":         "RunCommand",
	"initial":         "Initial",
	"new_compute":     "NewCompute",
	"compute":         "GetCompute",
	"suspend":         "Suspend",
	"resume":          "Resume",
	"kill_compute":    "KillCompute",
	"delete_snapshot": "DeleteSnapshot",
	"command_compute": "RunCommandCompute",
	"resume_compute":  "ResumeCompute",
	"observe":         "Observe",
}

func operationMethod(wire string) string { return operationMethods[wire] }
func operationWire(method string) string {
	for wire, name := range operationMethods {
		if name == method {
			return wire
		}
	}
	return ""
}
