package cli

// Help for lit context, composed into the shared help by grammar_docs.go and
// command_requirements.go.

const contextSummary = "Manage the project remembered for this checkout"

const contextFormatUsage = "Output `FORMAT`: cli, markdown or json (default cli; no session preference applies)"

const contextGroupHelp = `Show or clear the project remembered for the current checkout: a machine-local
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

These commands are offline: they never contact lit-server and need no logical
session. They ignore LIT_SESSION and LIT_ENDPOINT.`

const contextExample = "  lit context show\n  lit context show --format json\n  lit context clear"

func contextDocs(path []string) (summary, description, example string) {
	if len(path) == 1 {
		return contextSummary, contextGroupHelp, contextExample
	}
	switch path[1] {
	case "show":
		return "Show the project remembered for this checkout",
			"Show the project remembered for this checkout.\n" +
				"The saved data is reported as not validated: show reads only the local record and never asks\n" +
				"lit-server, so the project may since have been renamed, archived or removed.\n" +
				"Nothing remembered is a successful, explicit empty result.", "  lit context show\n  lit context show --format json"
	}
	return "Forget the project remembered for this checkout",
		"Forget the project remembered for this checkout.\n" +
			"clear removes only this checkout's preference. It does not delete projects, disconnect sessions,\n" +
			"release claims or touch pending requests, session associations or other workflow state.", "  lit context clear"
}

func contextRequirements(path string) (string, string) {
	prefix := "lit " + path
	form := prefix + " [--format cli|markdown|json]"
	if path == "context show" {
		return "Required flags: none. No session, endpoint or service is needed; the command works offline.\n" +
			"The saved preference is reported with validated: false (it is not checked against lit-server).\n" +
			"With nothing remembered it succeeds with remembered: false and the message No project remembered for this checkout.\n" +
			"A damaged or unsupported record fails with invalid_local_state (exit 1) and stays in place; lit context clear removes it.\n" +
			"Output: --format cli (default), markdown or json.", form
	}
	return "Required flags: none. No session, endpoint or service is needed; the command works offline.\n" +
		"The command is idempotent: clearing when nothing is remembered succeeds with outcome already_satisfied and creates nothing.\n" +
		"A damaged or unsupported record is removed too (removed: damaged_record), which is how to recover from invalid_local_state.\n" +
		"A change to this checkout's preference by another invocation is awaited briefly; a lock held longer fails with context_locked (exit 3).\n" +
		"Output: --format cli (default), markdown or json.", form
}
