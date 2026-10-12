package cli

import (
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
)

// Help documentation for the grammar, composed from the command catalog
// (command_catalog.go) and the requirement notes (command_requirements.go).

const rootDescription = "Track projects, issues, comments and work claims. Commands never start the service.\n" +
	"Start with connect [--session NAME], then select a project with session set.\n" +
	"Session selection: --session > LIT_SESSION. Unnamed connect creates a random session.\n" +
	"Run a leaf command's --help\nfor required inputs, optional flags and valid combinations."

var topSummaries = map[string]string{
	"connect": "Initialize or resume a client session", "disconnect": "End a connected client session", "version": "Print the binary version",
	"session": "Inspect or change session preferences", "projects": "Create and discover project containers", "issues": "Track work, state and relationships",
	"milestones": "Group project issues into named worksets", "comments": "Discuss issues with durable comments", "claims": "Reserve issues with expiring work claims",
	"transactions": "Reconcile uncertain mutation outcomes", "grep": "Search current content using a literal substring",
	"setup-skills": "Install embedded skills for Codex or Claude", "workflow-session": "Bind explicit agent runtime sessions to lit clients",
	"completion": "Generate the autocompletion script for the specified shell",
	"tui":        "Open the terminal UI (lit-tui)",
	"search":     "Search current content ranked by relevance",
}

var workflowSummaries = map[string]string{
	"fresh": "Create an isolated logical client", "subagent": "Create an isolated child client", "resume": "Resume the recorded client",
	"reconcile": "Inspect a pending session binding", "run": "Run a tracker command with the bound client", "checkout": "Record a repository checkout path",
}

// documentGrammar fills in every summary, description, example and flag usage.
func documentGrammar(root *nwcli.Command) {
	root.Summary, root.Description = "Track projects and issues with lit", rootDescription
	root.Walk(func(path []string, c *nwcli.Command) {
		switch {
		case len(path) > 0 && path[0] == "completion":
			c.Summary, c.Description = completionDocs(path)
		case len(path) > 0:
			c.Summary, c.Description, c.Example = commandDocs(path, len(c.Commands) > 0)
		}
		for i := range c.Flags {
			c.Flags[i].Usage = flagUsage(path, c.Flags[i])
			for j := range c.Flags[i].Fields {
				c.Flags[i].Fields[j].Usage = flagUsage(path, c.Flags[i].Fields[j])
			}
		}
	})
}

// completionDocs explains how to load each shell's completion script.
func completionDocs(path []string) (summary, description string) {
	if len(path) == 1 {
		return topSummaries["completion"], "Generate the autocompletion script for lit for the specified shell: bash, fish or zsh.\nSee each sub-command's help for details on how to use the generated script."
	}
	summary = "Generate the autocompletion script for " + path[1]
	switch path[1] {
	case "bash":
		return summary, summary + " (bash 4.4 or later).\n\nTo load completions in your current shell session:\n\n\tsource <(lit completion bash)\n\nTo load completions for every new session, add that line to ~/.bashrc."
	case "zsh":
		return summary, summary + " (zsh 5.8 or later).\n\nIf shell completion is not already enabled in your environment, enable it once:\n\n\tautoload -U compinit; compinit\n\nTo load completions in your current shell session:\n\n\tsource <(lit completion zsh)\n\nTo load completions for every new session, save the script as _lit in a directory on $fpath,\nfor example:\n\n\tlit completion zsh > \"${fpath[1]}/_lit\"\n\nStart a new shell for this setup to take effect."
	}
	return summary, summary + " (fish 3.4 or later).\n\nTo load completions in your current shell session:\n\n\tlit completion fish | source\n\nTo load completions for every new session, run once:\n\n\tlit completion fish > ~/.config/fish/completions/lit.fish"
}

func commandDocs(path []string, group bool) (summary, description, example string) {
	name := path[0]
	switch {
	case len(path) == 2 && name == "workflow-session":
		summary = workflowSummaries[path[1]]
		description = summary + ".\nOutput is a JSON association record; --format does not apply."
		if path[1] == "run" {
			description = summary + ".\nOutput is forwarded from the tracker command; the adapter supplies --format json.\nHelp and version retain their own text output.\nUse the direct lit tracker command for CLI or Markdown output."
		}
		example = "  lit workflow-session --host codex --runtime-id SESSION " + path[1] + map[string]string{
			"run": " -- issues list", "checkout": " --repository github.com/example/repo --path /work/repo", "subagent": " --parent-runtime-id PARENT"}[path[1]]
	case len(path) == 2:
		summary, description, example = commandSummary(name, path[1]), commandLong(name, path[1]), commandExample(name, path[1])
	case name == "workflow-session":
		summary = topSummaries[name]
		description = "Bind explicit runtime identity to durable workflow state. Never starts a service.\nFresh sessions and subagents get isolated clients; resume reuses the recorded client.\nLifecycle commands output a JSON association record. run forwards tracker output with --format json;\n--format does not apply to this adapter."
	case name == "grep":
		summary = topSummaries[name]
		description = "Search the selected project, or explicitly use --all-projects. Matching is case-insensitive by default.\nUse -- before patterns beginning with a dash; grep -- --help searches for literal --help."
		example = "  lit grep --session my-session 'renewal'\n  lit grep --session my-session -- --help"
	case name == "tui":
		summary, description = topSummaries[name], tuiHelp
		example = "  lit tui\n  lit tui --endpoint http://127.0.0.1:7411"
	case name == "search":
		summary, description = topSummaries[name], rankedSearchHelp
		example = "  lit search --session my-session 'connection timeout'\n  lit search --session my-session --all-projects --state open '\"claim renewal\" -flaky'"
	case name == "setup-skills":
		summary, description = topSummaries[name], setupSkillsHelp
		example = "  lit setup-skills --scope local --agent both\n  lit setup-skills --scope custom --path /work/project --agent codex"
	default:
		summary = topSummaries[name]
		if !group {
			example = "  lit " + name + " --session my-session"
			if name == "version" {
				example = "  lit version"
			}
		}
	}
	if group {
		return summary, description, example
	}
	return summary, leafDescription(strings.Join(path, " "), summary, description, &example), example
}

// leafDescription appends the requirement notes, reference rules and valid
// forms to a runnable command's description.
func leafDescription(path, summary, long string, example *string) string {
	requirements, forms := requirementsFor(path)
	if path == "connect" {
		requirements += "\n--session-id-only prints only the logical session name plus a newline on success, not the client UUID.\nIt overrides cli/markdown/json rendering for this call; --format and --output-format retain their normal preference effects.\nErrors and warnings go to stderr; failures return a nonzero status."
		forms += "\nlit connect [--session NAME] --session-id-only"
		*example += "\n  set -gx LIT_SESSION (lit connect --session-id-only)  # fish, current shell"
	}
	indent := func(s string) string { return strings.ReplaceAll(s, "\n", "\n  ") }
	if long = strings.TrimSpace(long); long == "" {
		long = summary + "."
	}
	long += "\n\nRequirements:\n  " + indent(requirements)
	if refs := referenceHelp(path); refs != "" {
		long += "\n\nReferences:\n  " + indent(refs)
	}
	long += "\n\nValid forms:\n  " + indent(forms) + "\n  [] means optional; | separates alternatives; ... means repeatable."
	if strings.Contains(forms, "FILTERS") || strings.Contains(forms, "ITEM_FLAGS") || strings.Contains(forms, "CHANGE_FLAGS") {
		long += "\n  FILTERS / ITEM_FLAGS / CHANGE_FLAGS refer to applicable flags below."
	}
	return long
}

// flagUsage is a flag's help text in a command: its catalog description, any
// command-specific wording, and whether it is required, conditional or optional.
func flagUsage(path []string, f nwcli.Flag) string {
	p := strings.Join(path, " ")
	u := flagDescriptions[f.Name]
	switch {
	case p == "milestones create" && f.Name == "issue":
		u = "Existing issue `REF` to include in this milestone (repeatable within each item)"
	case p == "issues list" && f.Name == "milestone":
		u = "Milestone `REF` whose members to include; requires project scope"
	case (p == "comments create" || p == "comments list") && f.Name == "issue":
		u = "Owning issue `ISSUE_REF`"
		if p == "comments create" {
			u += "; repeat to begin another atomic comment item"
		}
	case (p == "claims renew" || p == "claims release") && f.Name == "all":
		u = strings.ToUpper(path[1][:1]) + path[1][1:] + " all claims owned by this session; conflicts with explicit targets and --project"
	case (p == "session get" || p == "session unset") && f.Switch:
		u = map[string]string{"get": "Show", "unset": "Clear"}[path[1]] + " session property " + f.Name
	case f.Name == "sort":
		u = "Sort `FIELD`: " + strings.Join(f.Value.Choices, ", ")
	case f.Name == "clear":
		u = "Clear `FIELD` (repeatable): " + strings.Join(f.Value.Choices, ", ")
	case p == "search" && f.Name == "limit":
		u = "Page size `N` (default 20, maximum 100)"
	case p == "search" && f.Name == "state":
		u = "Only issues in `STATE`: open or closed"
	case p == "tui" && f.Name == "endpoint":
		u = "Service `URL` passed to lit-tui; flag > LIT_ENDPOINT > http://127.0.0.1:7411"
	case p == "workflow-session checkout" && f.Name == "path":
		u = "Required checkout `DIRECTORY` on this machine"
	case p == "workflow-session checkout" && f.Name == "repository":
		u = "Required repository `REF` to associate with this checkout"
	case p == "workflow-session subagent" && f.Name == "parent-runtime-id":
		u = "Required parent runtime session `ID`"
	case p == "workflow-session" && f.Name == "host":
		u = "Required agent `HOST`: codex or claude"
	case p == "workflow-session" && f.Name == "runtime-id":
		u = "Required explicit vendor runtime session `ID`"
	}
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "Required") && !strings.HasPrefix(u, "Conditional") {
		u = "Optional. " + u
	}
	switch requirement := flagRequirements(p)[f.Name]; {
	case requirement == "":
	case p == "transactions status" && f.Name == "file":
		u = requirement
	case requirement == "Required":
		u = requirement + " " + strings.TrimPrefix(strings.TrimPrefix(u, "Optional. "), "Required ")
	default:
		u = requirement + ". " + strings.TrimPrefix(strings.TrimPrefix(u, "Optional. "), "Required ")
	}
	return u
}

const tuiHelp = "Open the terminal UI for people: the separate lit-tui executable.\n" +
	"lit looks for lit-tui next to its own executable first, then on PATH, and hands\n" +
	"the terminal over to it (on Unix lit is replaced by lit-tui). --endpoint is passed through.\n" +
	"Bare lit with no command opens it too, but only when stdin and stdout are terminals;\n" +
	"otherwise bare lit reports that a subcommand is required, so scripts are unaffected.\n" +
	"Install lit-tui with: " + tuiInstall
