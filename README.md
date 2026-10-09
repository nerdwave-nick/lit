# nerdwave-workflows

The home for my coding agent workflows, local tooling, and customized skills.

## Components

- **`lit-server` — local issue tracker service:** one service per user on a machine,
  coordinating shared projects and issues stored as Markdown files.
- **`lit`:** the command-line client for `lit-server`, with terminal, Markdown and JSON
  output for people and coding agents.
- **`lit-tui`:** the terminal UI for people. `lit tui`, or bare `lit` on a terminal,
  starts it.
- **`internal/workflow-skills/`:** skills collected from various sources and adjusted to fit my
  coding workflows and preferences.

Projects represent features or other efforts: each can span multiple repositories,
and each repository can participate in multiple projects. Explicit work claims
and lease renewal coordinate concurrent coding agents; renewal is user/agent driven.

The initial implementation includes the Go service, command-line client, and
sixteen agent skills for Codex and Claude. Use the installed executable help for
command syntax. See [build, installation, and manual update instructions](docs/installation.md)
for supported binaries, per-user installation, foreground operation, and the
optional systemd user unit. Build or install with the standard Go toolchain:

```sh
go install ./cmd/lit ./cmd/lit-tui
lit setup-skills --scope local --agent both
```

Install `lit-tui` next to `lit`: `lit` looks for it in its own directory first, then
on `PATH`. Outside a terminal, bare `lit` still exits 2 with `invalid_arguments`, so
scripts and agents are unaffected.

The CLI embeds the skill suite; setup works offline without a checkout. Choose
`--scope user` for your home or `--scope custom --path /path/to/parent` for a
custom parent of `.codex` and `.claude`. Remote `go install` instructions and
publication caveats are in the installation guide.

`consult` is an installed skill, not a `lit` command. It guides agents to
invoke Claude or Codex directly, reuse explicit vendor sessions, and record
findings in discussion notes. Install the chosen vendor CLI separately; external
consultations still require authorization.

On Linux, install the daemon with `go install ./cmd/lit-server`. Start it with
`lit-server`, then connect locally with
`lit connect --session poc-human --actor-name Alex --actor-kind human`.
Without `--session` or `LIT_SESSION`, `connect` creates and displays a random
throwaway session name. Reuse that name explicitly for subsequent tracker calls.
The client defaults to `http://127.0.0.1:7411`; `--endpoint`, `LIT_ENDPOINT`, and
a session's remembered endpoint take precedence, in that order. Commands do not
start the service automatically.
