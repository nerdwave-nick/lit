package cli

import "strings"

// Help for lit context, composed into the shared help by grammar_docs.go and
// command_requirements.go.

const contextSummary = "Manage the project remembered for this checkout"

const contextFormatUsage = "Output `FORMAT`: cli, markdown or json (default cli; no session preference applies)"

const contextGroupHelp = `Remember, show, validate or forget the project for the current checkout: a machine-local
preference recording which lit project, on which lit-server, you chose for one checkout.
It is project context only: no client identity, claim token or session name is stored,
so a new agent conversation still gets its own client.

A checkout is the Git worktree root containing the current directory: every
subdirectory shares it, linked worktrees and each nested repository are separate
checkouts, and symlinked paths resolve to the same physical checkout. Outside Git
the checkout is the exact current directory; parents are never searched. A moved
checkout has a new path and remembers nothing until the project is remembered again.

Records are versioned files in checkout-context/ below the workflow state directory
(LIT_WORKFLOW_STATE_DIR; otherwise ${XDG_STATE_HOME:-~/.local/state}/lit/local on Linux,
~/Library/Application Support/lit/local on macOS, %LOCALAPPDATA%\lit\local on Windows),
separate from session associations, checkout-location maps, pending requests and
output caches. Changes are atomic and serialized per checkout; the last one wins.

No command needs a logical session, and all ignore LIT_SESSION. show and clear are offline:
they never contact lit-server. Only set and show --validate do, with one read-only request
that carries no client identity, so no client, claim or session is created. The endpoint is
--endpoint, then LIT_ENDPOINT, then (validating) the saved endpoint, then http://127.0.0.1:7411.
A different address reaching the same service is accepted; a different service ID is not.`

const contextExample = "  lit context set --project feat/tracker\n  lit context show\n  lit context show --validate\n  lit context clear"

const contextShowHelp = `Show the project remembered for this checkout.
Without --validate, show is offline: it reads only the local record and reports the saved data as
not validated, since the project may since have been renamed, archived or removed. It works while lit-server
is down. Nothing remembered is a successful, explicit empty result.
With --validate, show also asks lit-server whether the remembered service and project still match
and reports the project's current identity and title. Neither form changes the preference: a
renamed project is reported as renamed and the saved title is not rewritten.`

const contextSetHelp = `Remember a project for this checkout.
set asks lit-server, in one read-only request without a session or client, to resolve the project, then
saves its stable ID, its current title, the answering service's ID and the endpoint used, replacing an
earlier valid preference for this checkout. The title is display text; the ID is the identity.
REF is a title (case-insensitive), a UUID, a unique UUID prefix, or the title: and id: forms.
Only set remembers a project: reading a project, selecting one for a session or passing --project to
another command does not. Nothing is saved, and the earlier preference is preserved, when validation
fails; set never falls back to another project, endpoint or service.`

const contextClearHelp = `Forget the project remembered for this checkout.
clear removes only this checkout's preference. It does not delete projects, disconnect sessions,
release claims or touch pending requests, session associations or other workflow state.`

func contextDocs(path []string) (summary, description, example string) {
	if len(path) == 1 {
		return contextSummary, contextGroupHelp, contextExample
	}
	switch path[1] {
	case "set":
		return "Remember a project for this checkout", contextSetHelp, "  lit context set --project feat/tracker\n  lit context set --project feat/tracker --endpoint http://127.0.0.1:7411"
	case "show":
		return "Show the project remembered for this checkout", contextShowHelp, "  lit context show\n  lit context show --format json\n  lit context show --validate"
	}
	return "Forget the project remembered for this checkout", contextClearHelp, "  lit context clear"
}

// contextFlagUsage describes the flags the context commands declare themselves.
func contextFlagUsage(path, name, catalog string) string {
	switch {
	case name == "format":
		return contextFormatUsage
	case path == "context set" && name == "project":
		return "Project `REF` to remember: title, UUID or unique UUID prefix (also the title: and id: forms)"
	case path == "context set" && name == "endpoint":
		return "Service `URL` to resolve the project on; flag > LIT_ENDPOINT > http://127.0.0.1:7411"
	case path == "context show" && name == "endpoint":
		return "Service `URL` to validate against, with --validate; flag > LIT_ENDPOINT > saved endpoint > http://127.0.0.1:7411"
	case path == "context show" && name == "timeout":
		return "Positive request `DURATION` with --validate, for example 30s (default 30s)"
	case name == "validate":
		return "Also check the saved project against lit-server (one request, no session); changes nothing"
	}
	return catalog
}

const contextRequestNotes = "A request is one GET to lit-server with no client identity: no client, claim, session or pending request is created.\n" +
	"Failures exit nonzero and leave the preference untouched; --timeout bounds the wait (default 30s):\n"

// contextFailures lists the failure codes of the command that contacts a service.
func contextFailures(path string) string {
	lines := []string{}
	if path == "context set" {
		lines = append(lines,
			"  not_found: no project has that title or ID (exit 1)",
			"  ambiguous_reference: a UUID prefix names several projects (exit 1)")
	} else {
		lines = append(lines,
			"  wrong_service: the service that answered is not the remembered one (exit 1); lit context set --project REF remembers a project on it",
			"  not_found: the remembered project no longer exists on the service (exit 1); lit context clear forgets it")
	}
	lines = append(lines,
		"  service_unavailable: lit-server could not be reached in time (exit 1); lit context clear works without it",
		"  incompatible_service, invalid_response: the service does not support project context or answered unusably (exit 1)",
		"  invalid_endpoint: --endpoint or LIT_ENDPOINT is not an HTTP(S) origin (exit 2); an unusable --endpoint never falls back to another",
		"  invalid_local_state: a damaged or unsupported record (exit 1); lit context clear removes it")
	return strings.Join(lines, "\n") + "\n" +
		"Archived projects are not reported yet: lit has no archive state, so every existing project is available (tracked in lit issue 0c14ae79)."
}

func contextRequirements(path string) (string, string) {
	prefix := "lit " + path
	format := " [--format cli|markdown|json]"
	switch path {
	case "context set":
		return "Required flag: --project REF. set needs no session and creates no client; it makes one request to lit-server.\n" +
				"Endpoint: --endpoint > LIT_ENDPOINT > http://127.0.0.1:7411. The saved endpoint is not consulted: set chooses the service,\n" +
				"and the service ID it saves is the one that answered.\n" +
				"A damaged or unsupported record stops set before the service is asked (invalid_local_state); lit context clear removes it.\n" +
				contextRequestNotes + contextFailures(path) + "\n" +
				"Output: --format cli (default), markdown or json; outcome applied, with the replaced preference as previous.",
			prefix + " --project REF [--endpoint URL] [--timeout DURATION]" + format
	case "context show":
		return "Required flags: none. Without --validate no session, endpoint or service is needed; the command works offline and\n" +
				"the saved preference is reported with validated: false.\n" +
				"With --validate: --endpoint > LIT_ENDPOINT > the saved endpoint > http://127.0.0.1:7411 picks the service; the remembered\n" +
				"service ID and project ID are checked in one request. --endpoint and --timeout apply only with --validate.\n" +
				"A validated project reports validated: true and current (service, project, title and the endpoint used); a project\n" +
				"renamed since it was saved reports renamed: true and the saved title is not rewritten.\n" +
				"With nothing remembered, show (and show --validate, which makes no network request) succeeds with remembered: false and the\n" +
				"message No project remembered for this checkout.\n" +
				contextRequestNotes + contextFailures(path) + "\n" +
				"Output: --format cli (default), markdown or json.",
			prefix + format + "\n" + prefix + " --validate [--endpoint URL] [--timeout DURATION]" + format
	}
	return "Required flags: none. No session, endpoint or service is needed; the command works offline.\n" +
		"The command is idempotent: clearing when nothing is remembered succeeds with outcome already_satisfied and creates nothing.\n" +
		"A damaged or unsupported record is removed too (removed: damaged_record), which is how to recover from invalid_local_state.\n" +
		"A change to this checkout's preference by another invocation is awaited briefly; a lock held longer fails with context_locked (exit 3).\n" +
		"Output: --format cli (default), markdown or json.", prefix + format
}
