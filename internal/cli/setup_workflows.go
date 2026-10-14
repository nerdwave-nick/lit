package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

const setupWorkflowsHelp = `setup-workflows --scope local|user --agent codex|claude|both
setup-workflows --agent codex|claude --config-dir DIRECTORY
Installs the embedded workflow skills and the managed instruction block offline; never starts lit-server.
Output is a JSON installation receipt; --format does not apply.
local: .codex/.claude in the current directory. user: .codex/.claude in the home directory.
--config-dir: one agent configuration directory, for example ~/.claude-work. It takes exactly one --agent,
installs into that one directory and must already exist. CLAUDE_CONFIG_DIR and CODEX_HOME are not read;
install each configuration with its own run, each with its own ownership record (.lit-workflows.json).
An install made by the removed setup-skills command (.lit-skills.json) is adopted in the same transaction.
Existing owned files upgrade; local edits and collisions fail.
Both agents are preflighted before writes; unexpected errors can leave one host completed. Rerun safely.
`

// installReceipt is the JSON result of a successful setup-workflows run.
type installReceipt struct {
	Installed []installedTarget `json:"installed"`
}

type installedTarget struct {
	Agent     string `json:"agent"`
	Scope     string `json:"scope"`
	Directory string `json:"directory"`
	Adopted   bool   `json:"adopted"` // the target held a setup-skills install
}

func runSetupWorkflows(p *nwcli.Parsed, out, errOut io.Writer) int {
	value := func(name string) string {
		if v := p.Flags[name]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	targets, e := workflowskills.ResolveTargets(workflowskills.Selection{Scope: value("scope"), Agent: value("agent"), ConfigDir: value("config-dir")})
	if e != nil {
		fmt.Fprintln(errOut, "setup-workflows:", e)
		return 2
	}
	results, e := workflowskills.Install(workflowskills.Bundle(), targets)
	if e != nil {
		fmt.Fprintln(errOut, "setup-workflows:", e)
		return 1
	}
	var r installReceipt
	for _, res := range results {
		r.Installed = append(r.Installed, installedTarget{res.Target.Host, res.Target.Scope, res.Target.Dir, res.Adopted})
	}
	if e = json.NewEncoder(out).Encode(r); e != nil {
		fmt.Fprintln(errOut, "setup-workflows: workflows installed, but could not write result:", e)
		return 1
	}
	return 0
}
