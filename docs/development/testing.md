# Verification

Run the active Go checks from the repository root:

```sh
go test -count=1 -race ./...
go vet ./...
go test -count=1 -v ./integration -run '^TestGoDistributionWalkthrough$'
```

The integration harness uses temporary service data, private client state, and
working directories. The distribution walkthrough installs the public binaries
(`lit`, `lit-server`, `lit-tui`) with `go install`, checks that bare `lit` outside a
terminal keeps its usage error while `lit tui` hands over to `lit-tui`, extracts
embedded skills offline, and exercises sessions,
claims, checkpoints, cross-checkout resume, and closure against a temporary service.
It does not invoke paid models or start a persistent service.

Installer and workflow-session regression tests live in
`internal/workflow-skills` and `internal/workflowsession`. Protocol fixtures in
`internal/protocol/testdata` pin canonical request bytes and hashes.

`integration/tui_dispatch_test.go` covers how `lit` finds and starts `lit-tui`
(sibling first, then PATH, process replacement, endpoint pass-through, the missing
binary error, and bare `lit` on a pseudo-terminal via util-linux `script`).
`internal/tui/tmux_test.go` drives the detail viewport in a private tmux server:
the `internal/tui` test binary re-executes itself as a demo program showing a
Markdown file (no service is contacted). It covers resizing mid-scroll, per-block
horizontal scroll and the plain markers under `TERM=dumb` and `NO_COLOR`. These
tests skip when tmux is not installed or with `-short`.

`integration/tui_lifecycle_test.go` builds `lit-tui` and drives it in a private
tmux server against a temporary `lit-server`, with its own HOME, XDG directories,
`LIT_STATE_DIR` and `LIT_ENDPOINT`: quit, Ctrl-C, SIGHUP (the tmux session is
killed), SIGTERM, `kill -9` then relaunch (the orphan is disconnected), an orphan
with fabricated uncertain evidence (not disconnected), a second live instance
(never disconnected) and an uncertain exit (entry kept, second keypress). The
SIGHUP and SIGTERM runs delay the disconnect behind a proxy, so the process must
wait for it. These scenarios skip without tmux or with `-short`. The lifecycle,
registry and recovery packages are tested in-process against a temporary
service (`internal/tui/tuitest`).

`cmd/lit/deps_test.go` uses `go list -deps` to prove that only `lit-tui` links the
Charm, Glamour and chroma packages.

Keep generated logs, agent transcripts, and session snapshots out of version
control. These checks do not establish native agent behavior or macOS/Windows
runtime compatibility.
