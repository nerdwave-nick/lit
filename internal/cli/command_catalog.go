package cli

var flagDescriptions = map[string]string{
	"milestone":       "Milestone `TITLE` (create: new title); repeat to begin another atomic item",
	"add-issue":       "Add issue `REF` to milestone membership (repeatable)",
	"remove-issue":    "Remove issue `REF` from milestone membership (repeatable)",
	"session-id-only": "Print only the logical session name and a newline on success; overrides output formatting, not saved preferences",
	"session":         "Conditional. Logical session `NAME`; --session > LIT_SESSION; see requirements", "endpoint": "Service `URL`; flag > LIT_ENDPOINT > remembered session > http://127.0.0.1:7411", "format": "Output `FORMAT`: cli, markdown or json (otherwise session preference)", "timeout": "Positive request `DURATION`, for example 30s (default 30s)",
	"client-id": "Existing client `UUID` to resume", "actor-name": "Actor display `NAME`", "actor-kind": "Actor `KIND`: human or agent", "project": "Project `REF`: title or UUID; otherwise session selection", "runtime-vendor": "Runtime vendor `NAME`", "runtime-session-id": "Vendor runtime session `ID`", "output-format": "Session output `FORMAT`: cli, markdown or json", "session-id": "Show the client session ID", "service-id": "Show the bound service ID", "status": "Show connection status", "revision": "Expected object revision `N`; reject stale mutations", "project-title": "New project `TITLE`; repeat to begin another atomic item", "project-id": "Project `REF`; repeat to begin another update item", "content": "UTF-8 content `TEXT`; conflicts with --content-file", "content-file": "Read UTF-8 content from `PATH`; - reads stdin once", "repository": "Repository `REF` associated with this project (repeatable)", "file": "Input `PATH`: mutation JSON array or typed query object; - reads stdin once", "title": "Object `TITLE` (exact title filter on list)", "add-repository": "Add repository `REF` (repeatable)", "remove-repository": "Remove repository `REF` (repeatable)", "clear": "Clear a `FIELD` (repeatable): content, labels, assignee, parent or repositories as applicable", "sort": "Sort `FIELD`: created-at, updated-at or title as applicable", "direction": "Sort `DIRECTION`: asc or desc", "limit": "Page size `N` (default 100, maximum 1000 unless configured)", "cursor": "Continue the same query with opaque `TOKEN`", "all": "Return all results as one bounded snapshot; conflicts with --limit/--cursor", "request-hash": "Find history for transaction `HASH`", "issue": "Issue `REF` (create: new title); repeat to begin another atomic item", "comment": "Comment `REF` to update; repeat to begin another atomic item", "parent": "Parent issue `REF`; list accepts none for no parent", "state": "Issue `STATE`: open or closed", "label": "Initial issue `LABEL` (repeatable)", "assignee": "Assignee `NAME`; list accepts none for unassigned", "author": "Comment author `NAME`", "add-label": "Add issue `LABEL` (repeatable)", "remove-label": "Remove issue `LABEL` (repeatable)", "from": "Source issue `REF`; repeat to begin another atomic relation group", "to": "Target issue `REF` in this relation group (repeatable)", "relation": "Relation `KIND`: blocks, blocked-by or related", "force": "Explicitly override competing claim ownership where supported", "all-projects": "Search or list across projects instead of the selected project", "id": "Match object `UUID` (repeatable)", "query": "Case-insensitive content substring `TEXT`", "created-after": "Inclusive creation boundary `RFC3339`", "created-before": "Exclusive creation boundary `RFC3339`", "updated-after": "Inclusive update boundary `RFC3339`", "updated-before": "Exclusive update boundary `RFC3339`", "repository-ref": "Match repository `REF` (repeatable)", "labels-all": "Require every specified `LABEL` (repeatable)", "labels-any": "Require any specified `LABEL` (repeatable)", "labels-none": "Exclude every specified `LABEL` (repeatable)", "blocked": "Filter blocked issues: `BOOL` true or false", "claimed": "Filter active work claims: `BOOL` true or false", "owner-client-id": "Filter claim owner by client `UUID`", "issue-id": "Filter claim by issue `UUID`", "for": "Lease `DURATION`, positive and at most 1h", "until": "Absolute lease expiry `RFC3339`, at most one hour from service time", "context": "Include `N` surrounding lines in search excerpts", "n": "Show line numbers in search excerpts", "case-sensitive": "Match case exactly (literal and regex)", "regex": "Match PATTERN as a Go RE2 regular expression, line by line", "scope": "Install `SCOPE`: local, user or custom", "agent": "Target `AGENT`: codex, claude or both", "path": "Parent `DIRECTORY` of .codex/.claude for custom scope", "host": "Agent `HOST`: codex or claude", "runtime-id": "Explicit vendor runtime session `ID`", "cli": "Path to lit `EXECUTABLE` (default: this binary)", "discussion-id": "Optional discussion `ID` for workflow metadata", "parent-runtime-id": "Parent runtime session `ID` for a new subagent",
}

func commandUsage(f, v string) string {
	if f == "projects" && v == "get" {
		return " [REF...]"
	}
	if f == "projects" && v == "history" {
		return " [REF]"
	}
	if f == "session" || f == "transactions" {
		return ""
	}
	switch v {
	case "get":
		return " REF..."
	case "history":
		return " REF"
	case "update", "close", "reopen":
		return " [REF...]"
	case "acquire":
		return " REF..."
	case "renew", "release":
		return " [REF...]"
	}
	return ""
}
func commandSummary(f, v string) string {
	switch v {
	case "create":
		return "Create " + f + " atomically"
	case "update":
		return "Update " + f + " atomically"
	case "get":
		return "Read " + f + " and current properties"
	case "list":
		return "List and filter " + f
	case "history":
		return "Read immutable mutation history"
	case "close":
		return "Mark issues closed"
	case "reopen":
		return "Mark issues open"
	case "link":
		return "Add issue relationships atomically"
	case "unlink":
		return "Remove issue relationships atomically"
	case "set":
		return "Set explicit session properties"
	case "unset":
		return "Clear optional session properties"
	case "status":
		return "Inspect retained requests without replaying them"
	case "acquire":
		return "Reserve issues with expiring exclusive claims"
	case "renew":
		return "Extend claims owned by this session"
	case "release":
		return "Release claims owned by this session"
	}
	return v
}
func commandLong(f, v string) string {
	s := commandSummary(f, v) + "."
	if v == "get" && (f == "projects" || f == "issues" || f == "comments" || f == "milestones") {
		s += "\nWith --format markdown, one complete returned record becomes YAML frontmatter\nplus the exact original Markdown body. Duplicate selectors may return one record;\nmultiple returned records use a structured report. Export each separately for documents.\nThis is a read snapshot, not an import format: --content-file reads the entire file as body."
	}

	switch v {
	case "list":
		s += "\nFilters combine with AND; set filters may repeat. --all conflicts with --limit/--cursor.\nReuse the same filters with a returned cursor. --all does not widen project scope."
	case "create", "update":
		s += "\nRepeat the item boundary flag to submit an atomic batch, or use --file with a JSON array.\nScalar fields may occur once per item; repeated set fields preserve input order."
	case "link", "unlink":
		s += "\nRepeat --from followed by --to targets and --relation for another atomic group.\nblocks points from source to target; blocked-by reverses it; related is symmetric."
	case "history":
		s += "\nUse a reference (title or UUID) and pagination, or select one --request-hash."
	case "status":
		s += "\nWithout flags, inspect all retained requests. --session filters by service/client binding.\nOutcomes: committed, already_satisfied, uncommitted, or uncertain (exit 4). Never replays requests."
	}
	return s
}
func commandExample(f, v string) string {
	p := "  lit " + f + " " + v + " --session my-session"
	if f == "issues" || f == "milestones" || f == "comments" && (v == "create" || v == "list") || f == "claims" && v != "list" {
		p += " --project test/poc"
	}
	switch v {
	case "create":
		switch f {
		case "projects":
			p += " --project-title test/poc --content 'Try lit'"
		case "issues":
			p += " --issue 'Verify the PoC' --content 'Exercise the tracker'"
		case "comments":
			p += " --issue 'Verify the PoC' --content 'Verified'"
		case "milestones":
			p += " --milestone M1 --content 'Verify the PoC'"
		}
	case "list":
		if f == "comments" {
			p += " --issue 'Verify the PoC'"
		}
		p += " --limit 20"
	case "get", "history", "update", "close", "reopen", "acquire", "renew", "release":
		if f != "session" {
			if f == "projects" {
				p += " test/poc"
			} else if f == "comments" {
				p += " 01234567-89ab-4cde-8fab-0123456789ab"
			} else if f == "milestones" {
				p += " M1"
			} else {
				p += " 'Verify the PoC'"
			}
		}
		if v == "update" {
			p += " --content 'Updated details'"
		}
	case "link", "unlink":
		p += " --from 'Implement API' --to 'Test API' --relation blocks"
	case "set":
		p += " --project test/poc"
	case "unset":
		p += " --project"
	}
	return p
}
