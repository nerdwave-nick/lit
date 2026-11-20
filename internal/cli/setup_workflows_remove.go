package cli

import (
	"encoding/json"
	"fmt"
	"io"

	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

// removalReceipt is the JSON result of a successful setup-workflows --remove run.
type removalReceipt struct {
	Removed []removedTarget `json:"removed"`
}

type removedTarget struct {
	Agent     string `json:"agent"`
	Scope     string `json:"scope"`
	Directory string `json:"directory"`
	// Found reports that the target held an installation; false means there was
	// nothing to remove.
	Found  bool `json:"found"`
	Skills int  `json:"skills"`
	Hooks  bool `json:"hooks"`
	// Mod reports that the lit-ui mod directory was removed.
	Mod bool `json:"mod,omitempty"`
	// StatusLine is "restored" (the wrapped original is back), "removed" or
	// "left" (changed after setup wrote it).
	StatusLine string   `json:"status_line,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

func runRemoveWorkflows(targets []workflowskills.Target, out, errOut io.Writer) int {
	results, e := workflowskills.Remove(targets)
	if e != nil {
		fmt.Fprintln(errOut, "setup-workflows:", e)
		return 1
	}
	var r removalReceipt
	for _, res := range results {
		t := removedTarget{Agent: res.Target.Host, Scope: res.Target.Scope, Directory: res.Target.Dir, Found: res.Found, Skills: res.Skills,
			Hooks: res.Hooks, Mod: res.Mod, StatusLine: res.StatusLine, Warnings: res.Warnings, Notes: res.Notes}
		r.Removed = append(r.Removed, t)
		reportTarget(errOut, t.Agent, t.Directory, t.Warnings, t.Notes)
	}
	if e = json.NewEncoder(out).Encode(r); e != nil {
		fmt.Fprintln(errOut, "setup-workflows: workflows removed, but could not write result:", e)
		return 1
	}
	return 0
}
