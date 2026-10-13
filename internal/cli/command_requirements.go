package cli

import "strings"

// These notes document the domain parsers, including conditional inputs that a
// flag's own description cannot express: saved selections and batches.
const sessionRequirement = "Required session: --session NAME or LIT_SESSION; connect that session first."
const issueScope = "Issue titles need project selection: --project REF or the session's selected project.\nIssue UUIDs and project-qualified issue references can identify existing issues without that selection."
const contentRules = "--content and --content-file are mutually exclusive per item; stdin (-) may be read only once."
const mutationInputs = "Choose one input mode: positional targets, boundary-flag groups, or --file PATH (JSON array).\nDo not mix --file with item flags or positional targets; grouped and positional targets cannot mix."
const listRules = "--all conflicts with --limit/--cursor; --all does not widen project scope.\nReuse the same filters with --cursor. Filters combine with AND."

func requirementsFor(path string) (string, string) {
	prefix := "lit " + path
	switch path {
	case "connect":
		return "Required flags: none.\nSession: --session > LIT_SESSION; with neither, create a random throwaway session name.\nAn explicitly supplied --session must not be empty; omit it to use the fallback.\nThe result includes items[0].logical_session (Logical session in readable output). Reuse the returned name explicitly with --session NAME (or set LIT_SESSION).\nThrowaway describes the generated name; it does not imply automatic expiry or deletion.\nNew sessions default to the system username (then USER, then human) and actor kind human.\nAn existing local session resumes its saved client; --client-id adopts an existing client\nor must match that saved client. Optional actor/runtime/project flags update its properties.\n--format also saves the output preference here unless --output-format is explicit.\nEndpoint is optional: --endpoint > LIT_ENDPOINT > remembered session > http://127.0.0.1:7411.", prefix + "\n" + prefix + " [--session NAME] [--actor-name NAME] [--actor-kind human|agent]\n" + prefix + " [--session NAME] --client-id UUID [--project REF]\n" + prefix + " [--session NAME] --format json --output-format cli"
	case "disconnect":
		return "Required flag when LIT_SESSION is unset: --session NAME.\n" + sessionRequirement, prefix + " [--session NAME]"
	case "version":
		return "Required flags: none. No session or service is needed.", prefix
	case "setup-skills":
		return "Required flags: --scope and --agent.\n--path is required with custom scope and forbidden with local or user scope.\nNo tracker session or service is needed.", prefix + " --scope local|user --agent codex|claude|both\n" + prefix + " --scope custom --path DIRECTORY --agent codex|claude|both"
	case "tui":
		return "Required flags: none. No session is needed; lit-tui must be installed next to lit or on PATH.\nlit tui needs an interactive terminal; lit-tui refuses to start without one.", prefix + " [--endpoint URL]\nlit [--endpoint URL]  # on a terminal"
	case "transactions status":
		return "Required flags: none. Without --file, inspect all retained request files.\n--file selects one retained request envelope (not a mutation array or stdin).\nOnly explicit --session filters by service/client binding; LIT_SESSION does not filter.\nEach retained request supplies its own endpoint; this command never replays mutations.", prefix + " [--session NAME]\n" + prefix + " --file PATH [--session NAME]"
	case "search":
		return "Required: one nonempty QUERY and project selection, or --all-projects.\n--project and --all-projects are mutually exclusive. Omitted --project uses session selection.\n" + sessionRequirement, prefix + " [--project REF | --all-projects] [--state open|closed] [--limit N] [--cursor TOKEN] QUERY\n" + prefix + " [--project REF | --all-projects] -- '-flaky timeout'"
	case "grep":
		return "Required: one nonempty PATTERN and project selection, or --all-projects.\n--project and --all-projects are mutually exclusive. Omitted --project uses session selection.\n" + sessionRequirement, prefix + " [--project REF | --all-projects] [-E] [--limit N] [--cursor TOKEN] PATTERN\n" + prefix + " [--project REF | --all-projects] -- --help"
	}
	if strings.HasPrefix(path, "workflow-session ") {
		return workflowRequirements(strings.TrimPrefix(path, "workflow-session "))
	}
	parts := strings.Fields(path)
	family, verb := parts[0], parts[1]
	if family == "session" {
		switch verb {
		case "get":
			return "No required property flags. Without property flags, show every session property, including the logical session name\nand its distinct client UUID. --session-id selects only the client UUID.\nProperty selectors take no values.\n" + sessionRequirement, prefix + " [--session NAME] [--project] [--actor-name] [--status]"
		case "set":
			return "Required: at least one property flag with a value.\nProperties: --project, --output-format, --actor-name, --actor-kind,\n--runtime-vendor, --runtime-session-id. Multiple properties may be set together.\n" + sessionRequirement, prefix + " --project REF [--output-format cli|markdown|json]\n" + prefix + " --actor-name NAME --actor-kind human|agent"
		case "unset":
			return "Required: at least one optional property selector (no value).\nChoose --project, --output-format, --runtime-vendor or --runtime-session-id.\nActor name and kind cannot be unset.\n" + sessionRequirement, prefix + " --project [--output-format]\n" + prefix + " --runtime-vendor --runtime-session-id"
		}
	}
	if family == "claims" {
		switch verb {
		case "list":
			return "No required query flags. Defaults to claims owned by this session.\nOptional --file supplies a query object; explicit flags override its corresponding fields.\n" + listRules + "\n" + sessionRequirement, prefix + " [--owner-client-id UUID] [--issue-id UUID] [--limit N] [--cursor TOKEN]\n" + prefix + " --all\n" + prefix + " --file PATH [--owner-client-id UUID]"
		case "acquire", "get":
			req := "Required: one or more issue REF operands.\n" + issueScope + "\n" + sessionRequirement
			form := prefix + " REF... [--project REF]"
			if verb == "acquire" {
				req += "\nLease: choose --for or --until, never both; default 30m, maximum 1h from service time."
				form += " [--for DURATION | --until RFC3339] [--force]"
			}
			return req, form
		case "renew", "release":
			req := "Required: one or more issue REF operands, or --all for this session's owned claims.\n--all cannot be combined with targets or --project.\n" + issueScope + "\n" + sessionRequirement
			suffix := ""
			if verb == "renew" {
				req += "\nLease: choose --for or --until, never both; default 30m, maximum 1h from service time."
				suffix = " [--for DURATION | --until RFC3339]"
			}
			return req, prefix + " REF... [--project REF]" + suffix + "\n" + prefix + " --all" + suffix
		}
	}
	scope := sessionRequirement
	if family == "issues" || family == "comments" && (verb == "create" || verb == "list") {
		scope += "\n" + issueScope
	}
	if family == "milestones" {
		const milestoneScope = "Milestone titles are resolved inside the selected project. A project-scoped UUID or ID prefix is valid; use the full UUID to avoid ambiguity."
		switch verb {
		case "get":
			return "Required: one or more milestone REF operands. Resolve the reference in the selected project.\n" + milestoneScope + "\n" + sessionRequirement, prefix + " REF..."
		case "history":
			return "Required: one milestone REF operand. Resolve the reference in the selected project.\n" + milestoneScope + "\n--request-hash conflicts with --limit, --cursor and --all.\n--all conflicts with --limit/--cursor.\n" + sessionRequirement, prefix + " REF [--limit N] [--cursor TOKEN]\n" + prefix + " REF --all\n" + prefix + " REF --request-hash HASH"
		case "list":
			return "Required: project scope via --project REF, saved session selection, or project_id in the query file. Milestone queries cannot span projects.\nChoose flag filters or a typed JSON query object: cannot combine --file with query/filter flags.\n" + listRules + "\n" + sessionRequirement, prefix + " [--project REF] [FILTERS] [--limit N] [--cursor TOKEN]\n" + prefix + " [--project REF] [FILTERS] --all\n" + prefix + " --file PATH"
		case "create":
			return "Required: project selection via --project REF or the session, and --milestone TITLE per flag-defined item; or --file PATH (nonempty JSON array with project per item or selected project).\nRepeat --milestone to begin another atomic item; repeat --issue REF for members in that item. --issue references resolve within the milestone project.\nContent is optional. " + contentRules + "\n" + sessionRequirement, prefix + " [--project REF] --milestone TITLE [--content TEXT | --content-file PATH] [--issue REF...]\n" + prefix + " [--project REF] --milestone TITLE [ITEM_FLAGS] --milestone TITLE [ITEM_FLAGS]\n" + prefix + " --file PATH"
		case "update":
			return "Required: one or more milestone REF operands, or --file PATH (nonempty JSON array).\nUse --title, --content/--content-file, --add-issue, --remove-issue, --clear content|issues and --revision as needed. Member references resolve within each target milestone's project. --file cannot combine with flag or positional inputs. " + contentRules + "\n" + milestoneScope + "\n" + sessionRequirement, prefix + " REF... [CHANGE_FLAGS] [--revision N]\n" + prefix + " --file PATH"
		}
	}
	switch verb {
	case "get", "history":
		refs := "REF..."
		requirement := "Required: one or more REF operands."
		if verb == "history" {
			refs = "REF"
			requirement = "Required: one REF operand."
		}
		if family == "projects" {
			refs = "[" + refs + "]"
			requirement = "Required: a project target, from REF, --project REF, or session selection."
		}
		if family == "comments" {
			requirement += " Comment references are UUIDs."
		}
		forms := prefix + " " + refs
		if verb == "history" {
			if family == "projects" {
				requirement += "\n--request-hash selects one entry; pagination flags are ignored in that mode."
			} else {
				requirement += "\n--request-hash conflicts with --limit, --cursor and --all."
			}
			requirement += "\n--all conflicts with --limit/--cursor."
			forms += " [--limit N] [--cursor TOKEN]\n" + prefix + " " + refs + " --all\n" + prefix + " " + refs + " --request-hash HASH"
		}
		return requirement + "\n" + scope, forms
	case "list":
		req := "No required query flags."
		forms := prefix + " [FILTERS] [--limit N] [--cursor TOKEN]\n" + prefix + " [FILTERS] --all\n" + prefix + " --file PATH"
		if family == "issues" {
			req = "Required: project selection (--project or saved session), or --all-projects.\n--all-projects conflicts with explicit --project; query files may supply project_id or all_projects."
			req += "\n--milestone REF is project-scoped and cannot be combined with --all-projects."
			forms = prefix + " [--project REF | --all-projects] [FILTERS] [--limit N] [--cursor TOKEN]\n" + prefix + " [--project REF | --all-projects] [FILTERS] --all\n" + prefix + " --file PATH"
		}
		if family == "comments" {
			req = "Required: an owning issue via --issue REF, or issue_id in the query file."
			forms = prefix + " --issue REF [FILTERS] [--limit N] [--cursor TOKEN]\n" + prefix + " --issue REF [FILTERS] --all\n" + prefix + " --file PATH"
		}
		return req + "\nChoose flag filters or a typed JSON query object: cannot combine --file with query/filter flags.\n" + listRules + "\n" + scope, forms
	case "create":
		boundary := "--project-title TITLE"
		if family == "issues" {
			boundary = "--issue TITLE"
		}
		if family == "comments" {
			boundary = "--issue ISSUE_REF"
		}
		req := "Required: " + boundary + " for each flag-defined item, or --file PATH (nonempty JSON array).\nDo not mix --file with item flags. Repeat the boundary flag before each item's fields.\nContent is optional. " + contentRules
		if family == "issues" {
			req += "\nNew issues require project selection: --project REF, the session's selected project,\nor a project field in each JSON item."
		}
		return req + "\n" + scope, prefix + " " + boundary + " [--content TEXT | --content-file PATH]\n" + prefix + " " + boundary + " [ITEM_FLAGS] " + boundary + " [ITEM_FLAGS]\n" + prefix + " --file PATH"
	case "update", "close", "reopen":
		boundary := "--project-id REF"
		if family == "issues" {
			boundary = "--issue REF"
		}
		if family == "comments" {
			boundary = "--comment REF"
		}
		req := "Required: target(s) using one of the forms below.\n" + mutationInputs
		fields := " [--revision N]"
		if verb == "update" {
			req += "\nUse change flags to set, add, remove or clear fields; unchanged values are safe no-ops. " + contentRules
			fields = " [CHANGE_FLAGS] [--revision N]"
		}
		positionalFields := fields
		if family == "projects" {
			positionalFields = " (CHANGE_FLAGS | --revision N)"
		}
		forms := prefix + " REF..." + positionalFields + "\n" + prefix + " " + boundary + fields + " [" + boundary + fields + "]\n" + prefix + " --file PATH"
		if family == "projects" {
			req += "\nWithout explicit targets, update uses --project REF or session selection.\nPositional/selected-project mode needs a change flag or --revision to define an item."
			forms += "\n" + prefix + " [--project REF] (CHANGE_FLAGS | --revision N)"
		}
		if verb == "update" {
			req += "\nCHANGE_FLAGS are the set/add/remove/clear flags listed below (for example --content TEXT)."
		}
		if family == "comments" {
			req += "\nComment references are UUIDs."
		}
		req += "\nRepeat the boundary flag before each item's fields; positional targets share one set of fields."
		return req + "\n" + scope, forms
	case "link", "unlink":
		return "Required: --from, at least one --to, and --relation per group; or --file PATH.\nDo not mix --file with group flags; positional targets are not accepted.\nRepeat --from to start another atomic group; --to may repeat within a group.\n" + scope, prefix + " --from REF --to REF --relation KIND [--to REF]\n" + prefix + " --file PATH"
	}
	panic("missing help requirements: " + path)
}

func workflowRequirements(action string) (string, string) {
	prefix := "lit workflow-session --host codex|claude --runtime-id ID " + action
	req := "Required flags: --host and --runtime-id."
	form := prefix
	switch action {
	case "fresh":
		form += " [--endpoint URL] [--project REF] [--discussion-id ID]"
	case "subagent":
		req = "Required flags: --host, --runtime-id and --parent-runtime-id.\nThe child runtime ID must be distinct from the parent runtime ID."
		form += " --parent-runtime-id PARENT [--endpoint URL] [--project REF] [--discussion-id ID]"
	case "checkout":
		req = "Required flags: --host, --runtime-id, --repository and --path.\nRequires a saved session with a selected project."
		form += " --repository REF --path DIRECTORY"
	case "run":
		req += "\nRequired operand: a tracker COMMAND after --. connect is forbidden.\nThe command cannot override --session, --endpoint, --client-id, --runtime-vendor,\n--runtime-session-id or --format; use the adapter lifecycle."
		form += " -- COMMAND [ARGS...]"
	}
	if action == "fresh" || action == "subagent" {
		req += "\nEndpoint: --endpoint > LIT_ENDPOINT > http://127.0.0.1:7411."
	} else {
		req += "\nUses the saved runtime binding and endpoint; --endpoint and --project do not apply."
	}
	return req, form
}

func flagRequirements(path string) map[string]string {
	m := map[string]string{}
	switch path {
	case "setup-skills":
		m["scope"] = "Required"
		m["agent"] = "Required"
		m["path"] = "Required with --scope custom; forbidden otherwise"
	case "session set", "session unset":
		for _, name := range []string{"project", "output-format", "runtime-vendor", "runtime-session-id"} {
			m[name] = "Optional individually; select at least one property"
		}
	case "transactions status":
		m["file"] = "Optional retained request envelope `PATH` (not stdin or a mutation array)"
	}
	if strings.HasPrefix(path, "workflow-session ") {
		for _, name := range []string{"parent-runtime-id", "repository", "path"} {
			if strings.HasSuffix(path, " subagent") && name == "parent-runtime-id" || strings.HasSuffix(path, " checkout") && name != "parent-runtime-id" {
				m[name] = "Required"
			}
		}
	}
	for _, name := range []string{"project-title", "project-id", "milestone", "issue", "comment", "from", "to", "relation", "file"} {
		if strings.Contains(path, " create") || strings.Contains(path, " update") || strings.HasSuffix(path, " link") || strings.HasSuffix(path, " unlink") || strings.HasSuffix(path, " close") || strings.HasSuffix(path, " reopen") {
			m[name] = "Conditional (see valid forms)"
		}
	}
	return m
}

// Selector rules belong with commands that consume existing references, not
// creation titles, opaque repository names, or UUID-only query filters.
func referenceHelp(path string) string {
	const project = "Project REF: exact title, UUID or unique UUID prefix.\nUse title:TITLE for literal title matching, or id:UUID_PREFIX for ID-only matching."
	const issue = "Issue REF: title in the selected project, UUID or unique UUID prefix.\nUse title:TITLE for a literal title, or id:UUID_PREFIX for ID-only matching.\nPROJECT_TITLE:ISSUE_REF selects an issue in another project, for example\nfeat/api:title:Review API. A qualified prefix is scoped to that project;\nan unqualified UUID or ID prefix is global even when a project is selected.\nFor titles containing slashes and colons, quote the argument and force title matching:\n--project test/poc 'title:Refactor a/b: cleanup'."
	const comment = "Comment REF: UUID, unique UUID prefix, or id:UUID_PREFIX.\nComments have no title selector or project-qualified selector."
	const milestone = "Milestone REF: exact title in the selected project, UUID, unique UUID prefix, title:TITLE, or id:UUID_PREFIX.\nIssue membership references resolve inside the selected milestone project."
	switch {
	case path == "connect" || path == "session set" || path == "grep" || path == "search" || path == "workflow-session fresh" || path == "workflow-session subagent":
		return project
	case strings.HasPrefix(path, "projects ") && path != "projects create" && path != "projects list":
		return project
	case strings.HasPrefix(path, "issues "):
		note := ""
		if path == "issues create" {
			note = "These rules apply to existing issue references such as --parent, not the new --issue title.\n"
		}
		return note + project + "\n" + issue
	case strings.HasPrefix(path, "milestones "):
		return milestone
	case path == "comments create" || path == "comments list":
		return "The comment owner selector identifies the owning issue.\n" + project + "\n" + issue
	case strings.HasPrefix(path, "comments "):
		return comment
	case strings.HasPrefix(path, "claims ") && path != "claims list":
		return project + "\n" + issue
	}
	return ""
}
