package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	workflowskills "github.com/nerdwave-nick/lit/internal/workflow-skills"
)

const setupWorkflowsHelp = `setup-workflows --scope local|user --agent codex|claude|both [--status line|mod|both]
setup-workflows --agent codex|claude --config-dir DIRECTORY [--status line|mod|both]
setup-workflows --remove --scope local|user --agent codex|claude|both
setup-workflows --remove --agent codex|claude --config-dir DIRECTORY
Installs the embedded workflow skills and the managed instruction block, and into an agent configuration (--scope user or --config-dir) the lit hooks and the Claude status surface (the status line, the lit-ui mod or both), offline; never starts lit-server.
Output is a JSON installation receipt on stdout (agent, scope, directory, adopted, hooks, status_line, status, mod, warnings, notes); --format does not apply. Warnings and notes are also printed on stderr.
local: .codex/.claude in the current directory; skills and the managed block only, never hooks, status line or mod. user: .codex/.claude in the home directory.
--config-dir: one agent configuration directory, for example ~/.claude-work. It takes exactly one --agent,
installs into that one directory and must already exist. CLAUDE_CONFIG_DIR and CODEX_HOME are not read;
install each configuration with its own run, each with its own ownership record (.lit-workflows.json).
Claude hooks: the lit-owned plugin directory skills/lit-hooks (SessionStart, SubagentStart, Stop). Claude status line: settings.json statusLine is set when unset or already lit's; a foreign one is wrapped as 'lit hook --host claude statusline --original COMMAND' and recorded.
--status chooses the Claude status surface, default both: line installs the status line only; mod installs the lit-ui mod only; both installs both.
The lit-ui mod (Claude Code 2.1.287 or later) is the lit-owned plugin directory skills/lit-ui next to lit-hooks; Claude Code loads it once the workspace is trusted. Setup generates its config.json ({"schema_version":1,"status":"mod"|"both","lit":"<absolute path of lit>"}), from which the mod reads its mode and the lit it runs.
With mod, a status line setup installed is restored to the original it wrapped or removed (status_line restored, removed or left when you changed it); a status line setup does not own stays. Rerunning with another --status converges: line removes the mod (mod "removed"), mod and both install it (mod "installed").
--status is ignored for Codex targets and for --scope local, with a note in that target's receipt.
Codex hooks: lit's groups are appended to hooks.json and recorded exactly. Codex skips them until you approve them in /hooks; setup never trusts hooks. It warns about [hooks] in config.toml and disabled hooks.
Hook commands run lit (the path of this executable when lit is not on PATH) and read LIT_ENDPOINT from the environment of the agent that runs them.
Setup warns about lit hooks found elsewhere (other configurations, the project's .claude and .codex, the hand-written ones) and never edits them.
An install made by the removed setup-skills command (.lit-skills.json) is adopted in the same transaction.
Existing owned files upgrade; local edits, collisions, symlinks and settings.json or hooks.json that are not valid JSON objects fail.
Both agents are preflighted before writes; unexpected errors can leave one host completed. Rerun safely.
--remove takes the same target flags and removes exactly what each target's ownership record lists: the skills, the managed instruction block, the lit-hooks plugin directory, the lit-ui mod, lit's groups in hooks.json and lit's status line (a wrapped original is restored exactly), then the record. It does not take --status. It uses the same lock, journal and recovery as installing, so an interrupted removal is rolled back by the next run. Owned files you changed are refused and nothing setup does not own is touched; settings.json, hooks.json and the instruction file setup created are deleted once only lit's content is left.
A target with no installation is reported ("nothing to remove") and is not an error; an install made by the removed setup-skills command (.lit-skills.json) is removed through that file. Output with --remove is a JSON receipt on stdout: {"removed": [{agent, scope, directory, found, skills, hooks, mod, status_line (restored, removed or left), warnings, notes}]}.
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
	// Hooks reports that the lit hooks belong to the installation (user scope
	// and configuration directories); StatusLine is "set", "wrapped",
	// "unchanged" or "kept" for a Claude configuration with hooks.
	Hooks      bool   `json:"hooks"`
	StatusLine string `json:"status_line,omitempty"`
	// Status is the Claude status surface (line, mod or both) and Mod what
	// became of the lit-ui mod ("installed" or "removed"), for a Claude
	// configuration with hooks.
	Status   string   `json:"status,omitempty"`
	Mod      string   `json:"mod,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Notes    []string `json:"notes,omitempty"`
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
	status, statusGiven := value("status"), len(p.Flags["status"]) > 0
	if statusGiven && status != "line" && status != "mod" && status != "both" {
		fmt.Fprintf(errOut, "setup-workflows: unknown --status %q: choose line, mod or both\n", status)
		return 2
	}
	if _, remove := p.Flags["remove"]; remove {
		if statusGiven {
			fmt.Fprintln(errOut, "setup-workflows: --status does not apply with --remove, which removes whatever the ownership record lists")
			return 2
		}
		return runRemoveWorkflows(targets, out, errOut)
	}
	binary := litBinary()
	results, e := workflowskills.InstallWith(workflowskills.Bundle(), targets, workflowskills.Options{Binary: binary, Status: status, LitPath: litPath()})
	if e != nil {
		fmt.Fprintln(errOut, "setup-workflows:", e)
		return 1
	}
	var r installReceipt
	for _, res := range results {
		t := installedTarget{Agent: res.Target.Host, Scope: res.Target.Scope, Directory: res.Target.Dir, Adopted: res.Adopted,
			Hooks: res.Hooks, StatusLine: res.StatusLine, Status: res.Status, Mod: res.Mod, Warnings: res.Warnings, Notes: res.Notes}
		if note := statusIgnored(res, statusGiven); note != "" {
			t.Notes = append(t.Notes, note)
		}
		if res.Hooks && binary != "lit" {
			t.Notes = append(t.Notes, "the hook commands call "+binary+" because the lit on PATH is not this executable; reinstall after moving or replacing it")
		}
		if endpoint := os.Getenv("LIT_ENDPOINT"); res.Hooks && endpoint != "" {
			t.Notes = append(t.Notes, "the lit hooks take LIT_ENDPOINT (here "+endpoint+") from the environment of the agent that runs them, not from setup; without it new sessions use http://127.0.0.1:7411")
		}
		r.Installed = append(r.Installed, t)
		reportTarget(errOut, t.Agent, t.Directory, t.Warnings, t.Notes)
	}
	if e = json.NewEncoder(out).Encode(r); e != nil {
		fmt.Fprintln(errOut, "setup-workflows: workflows installed, but could not write result:", e)
		return 1
	}
	return 0
}

// reportTarget prints a target's warnings and notes for the person running
// setup; stdout stays the JSON receipt.
func reportTarget(w io.Writer, agent, directory string, warnings, notes []string) {
	if len(warnings) == 0 && len(notes) == 0 {
		return
	}
	fmt.Fprintf(w, "%s %s\n", agent, directory)
	for _, warning := range warnings {
		fmt.Fprintln(w, "  warning:", warning)
	}
	for _, note := range notes {
		if strings.HasPrefix(note, " ") {
			fmt.Fprintln(w, "   ", strings.TrimSpace(note))
		} else {
			fmt.Fprintln(w, "  note:", note)
		}
	}
}

// statusIgnored is the note for a target --status does not apply to: Codex,
// and a local scope, which installs neither the mod nor the status line.
func statusIgnored(res workflowskills.Result, given bool) string {
	switch {
	case !given:
		return ""
	case res.Target.Host == "codex":
		return "--status was ignored: it chooses the Claude status surface, and Codex has neither the lit-ui mod nor a status line"
	case res.Target.Scope == workflowskills.ScopeLocal:
		return "--status was ignored: a local scope installs neither the lit-ui mod nor the status line; use --scope user or --config-dir"
	}
	return ""
}

// litBinary is how installed hook commands name lit: plainly as "lit" when
// the lit found through PATH is the running one, so the commands survive
// upgrades and moves, otherwise by the running executable's path.
func litBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "lit"
	}
	return resolveLitBinary(exe, exec.LookPath)
}

func resolveLitBinary(exe string, lookPath func(string) (string, error)) string {
	if found, err := lookPath("lit"); err == nil && sameFile(found, exe) {
		return "lit"
	}
	return exe
}

// litPath is the absolute path the lit-ui mod runs lit by: the lit found
// through PATH when it is the running one (a link there survives upgrades),
// otherwise the running executable.
func litPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "lit"
	}
	return resolveLitPath(exe, exec.LookPath)
}

func resolveLitPath(exe string, lookPath func(string) (string, error)) string {
	if found, err := lookPath("lit"); err == nil && sameFile(found, exe) {
		if abs, err := filepath.Abs(found); err == nil {
			return abs
		}
	}
	return exe
}

func sameFile(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}
