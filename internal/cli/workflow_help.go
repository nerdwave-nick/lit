package cli

// Help for the workflow-session actions that choose or keep a session's
// project, composed into the shared help by grammar_docs.go and
// command_requirements.go.

const selectionJSON = `The record's project_selection says how the project was chosen when the session was created:
  source "explicit": --project named it. The remembered project was not read.
  source "remembered": the project remembered for this checkout was validated and adopted. checkout, project_id,
    project_title (as validated now), saved_at and, only if the project was renamed since it was remembered, saved_title describe it.
  source "none": no project is selected. checkout names the checkout that was consulted when nothing is remembered for it.`

const freshDetails = `

A new session adopts the project remembered for the current checkout, and announces it in project_selection, unless
--project names one. Remember a project with lit context set; lit context show tells what a checkout has.
With --project REF the command selects that project and leaves the remembered project untouched: it is not read, validated,
adopted or rewritten, even when it is stale. Without --project the remembered project is validated against the service in
one request that registers nothing (the same check as lit context show --validate) and then selected in the new session;
with nothing remembered the session has no project selected, as before. A different runtime ID always gets a client of its own.

` + selectionJSON + `

A remembered project that cannot be validated stops the command with exit 1 before any association or client is created,
and it never falls back to another project, endpoint or service. The error names why (the record is damaged or unsupported,
the service is unreachable or another service, the project no longer exists) and the way out: lit context clear to forget the
project, or --project REF to start this conversation with an explicit project. The remembered project is left as it is.

The endpoint of an adopted project is --endpoint, then LIT_ENDPOINT, then the endpoint saved with the remembered project,
then http://127.0.0.1:7411. A different address that reaches the same service is accepted, a different service ID is not.
The session is registered at the endpoint that answered and keeps it for resume. With --project, or with nothing remembered,
the endpoint is --endpoint, then LIT_ENDPOINT, then the default. Later changes to the remembered project never redirect an
existing session.`

const subagentDetails = `

A subagent never adopts a remembered project: its project scope is the explicit --project its coordinator gives it, otherwise
none, and the remembered project of its checkout is not read. The record's project_selection is source "explicit" or "none".`

const resumeDetails = `

The association keeps the project_selection it was created with, kept as it was: resume does not read or validate the
remembered project, and a later lit context set or clear neither redirects the session nor makes resume fail.`

const showDetails = `

show prints the runtime's stored association record in the JSON the lifecycle commands print (exit 0); a pending
association prints with status "pending". It takes no adapter lock, runs no tracker command, never connects or contacts
the service and writes no state, so it answers while another workflow-session call, such as run, holds the runtime's lock.

A runtime without an association exits 3 and prints a JSON error object on stdout, with nothing on stderr:
  {"error":{"code":"association_not_found","message":"...","details":{"host":"claude","runtime_id":"ID"}}}
A record that cannot be read or validated exits 1 with the reason on stderr; it is left as it is.`

// workflowDetails are appended to the description of a workflow-session action.
var workflowDetails = map[string]string{"fresh": freshDetails, "subagent": subagentDetails, "resume": resumeDetails, "show": showDetails}

const freshProjectUsage = "Select this project `REF`: title or UUID; the remembered project is then not adopted and stays untouched"

const subagentProjectUsage = "Project `REF` for the child; the checkout's remembered project is never adopted"

const freshEndpointRule = "Endpoint: --endpoint > LIT_ENDPOINT > http://127.0.0.1:7411; an adopted remembered project uses\n--endpoint > LIT_ENDPOINT > the endpoint saved with it > http://127.0.0.1:7411."
