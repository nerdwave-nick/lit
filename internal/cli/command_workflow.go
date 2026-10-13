package cli

import (
	"io"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// runWorkflow hands a parsed adapter invocation to the workflow session runner
// in its own grammar: host flags, the action, the action's flags, and for run
// the tracker command after --.
func runWorkflow(p *nwcli.Parsed, out, errOut io.Writer) int {
	action := p.Path[len(p.Path)-1]
	args := append(flagArgs(p, "host", "runtime-id", "cli"), action)
	args = append(args, flagArgs(p, "project", "discussion-id", "parent-runtime-id", "repository", "path", "endpoint")...)
	if action == "run" {
		args = append(append(args, "--"), p.Operands...)
	}
	return workflowsession.Run(args, out, errOut)
}
