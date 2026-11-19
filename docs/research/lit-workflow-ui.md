# Interactive lit UI inside agent sessions

Investigated 2026-10-14. Question: how could an agent session show the current lit
project, tracker health, errors and claim counts, and let the human start workflows
(planner, to-spec, to-tickets, impl, orchestrate) by selecting them? What else
would such a UI make possible? This is evidence and a proposal set, not an
approved specification.

## Conclusion

Build it as a **Claude Code mod** that only presents and launches, layered on the
settled `lit hook` / status line / workflow-stage / remembered-project
specification. The tracker already decided this direction: "yes, later, as a UI
extension on the same lit commands ... all behavior stays in the shared lit
commands; the mod only presents it" ([decision 36155bd6], closed 2026-10-13).

- Mods are the only Claude Code surface with clickable controls in a live
  session: panes, a band above the prompt, a status entry, toasts, and `Button`,
  `Select`, `Input` elements. The status line is display-only.
- A mod can start a workflow without a model turn spent on menus:
  `$.command.run` runs a slash command "as if the person typed" it, and
  `$.prompt.fill` puts it in the prompt box for review. The prototype observed
  both starting installed skills (see Prototype results).
- The data is cheap: local reads measured at 5–60 ms, so 5–10 s polling plus
  event-triggered refresh is affordable without server changes.
- Prerequisite: the mod must map its Claude session to a lit association. That
  only becomes reliable once `lit hook session-start` uses the Claude
  `session_id` as the runtime ID (spec §1). Today about half of the stored
  associations use deliberately assigned IDs instead.
- Codex has no custom status line or mod API. Parity, if wanted, belongs in
  `lit-tui` as a launcher, not in the agent host.

## Existing decisions that constrain the design

All from project `feat/workflow-pocket-carryover` (shared specification, settled
2026-10-14), read through `lit projects get`:

| Topic | Settled behavior | Effect on the UI |
| --- | --- | --- |
| Mod (§ Out of scope, [decision 36155bd6]) | Later UI extension; Claude-only; behavior stays in lit commands. | The mod calls `lit` commands; it adds no tracker logic. |
| `lit hook` (§1) | `session_id` is the workflow-session runtime ID; hooks never acquire, renew, reacquire, force or release claims. | Gives the mod a deterministic association lookup. Automatic claim actions stay out of scope. |
| Status line (§6) | `🔥 lit \| <project> \| claims:N \| <stage>`, `⚫ not lit`, `lit: pending`, hook-failure indicator; cached; ~300 ms budget. | The mod must not duplicate this line; see open question 1. |
| Workflow stage (§7) | Server-side session property; outer stage only; cleared on completion or handoff. | The mod can show "running: impl" and disable conflicting launches. |
| Remembered project (§9) | `lit context set/show/clear`; never infer a project from cwd, branch, recency or one search result. | The picker may suggest, sort and pre-select the remembered project, but never auto-selects a guess. |
| Lease helper ([agent-launcher idea](../ideas/agent-launcher.md)) | Deferred; no automatic renewal or takeover. | Renewal buttons, if any, are explicit human actions (open question 3). |

Related open tickets: "Claude status line command" and "setup-workflows installs
hooks and the status line" in the same project.

## Verified capabilities

### Claude Code 2.1.296 (installed), mod API

Source: this build's generated API declarations (`claude-code.d.ts`, written by
the bundled plugin-authoring skill) and the
[mods overview](https://code.claude.com/docs/en/plugins/mods/overview),
[interface](https://code.claude.com/docs/en/plugins/mods/interface) and
[API](https://code.claude.com/docs/en/plugins/mods/api) pages. Mods need terminal
v2.1.287 or later.

| Need | API | Notes |
| --- | --- | --- |
| Persistent compact display | `$.ui.status(text)`; `ui.render` on `AbovePrompt` (band) | The band hides with `[-]`. |
| Rich interactive view | `$.ui.open({id,title})` pane, drawn by `ui.render` on `Pane` | Docked beside the transcript in fullscreen at ≥110–144 columns, inline otherwise. |
| Controls | `Button` (`onPress`), `Select` (`onSelect`, `autofocus`), `Input` (`onSubmit`), `Link`, `Markdown` | Mobile lacks `Input` and `Select`. Terminal and desktop also offer `Client`: a plugin module with its own state, timers and pointer events (custom widgets such as a graph). |
| Start a workflow | `$.command.run({command, args})` | Queued until the session is idle; runs through `command.run` hooks; "as if the person typed `/command args`". `CommandSource` includes `user` for "the user's or project's own file". |
| Stage a prompt for review | `$.prompt.fill({text})`, `$.prompt.submit({text, asUser})`, `$.prompt.suggest({text})` | `fill` lets the human edit and press Enter; `suggest` shows a next-step hint. |
| Read lit | `$.process.run(argv, {timeoutMs})` | No shell; 30 s default timeout; CLI hosts only. |
| Refresh | `$.clock.every(ms, fn)`; `tool.call` hooks | A hook on Bash calls that run `lit ...` can refresh right after the agent changes claims or state. |
| Session identity | `$.session.id()`, `$.session.cwd()` | The ID is the transcript file name, which is the hook `session_id`. |
| Own commands, no model turn | `$.command.register({name})` + `command.run` hook returning `{text}` | For example `/lit-ui` to open the pane. |
| Notify | `$.ui.toast(text)`; `$.audio.play` | |
| Persist | `$.state` (session), `$.store` (across sessions) | |

Limits: mods draw only in the terminal and the Desktop Code tab. `claude -p`, the
Agent SDK and the VS Code chat panel run hooks but draw nothing.

### Status line, launch and observation

From the [status line docs](https://code.claude.com/docs/en/statusline): the
status line is display-only. OSC 8 links open a browser but cannot run a command.
`refreshInterval` must be at least 1 s. Non-zero exit or empty output blanks the
bar. `claude "/planner ..."` starts a session with a skill
([CLI reference](https://code.claude.com/docs/en/cli-reference)).
`claude agents --json [--all]` reports per-session `state`/`status`/`waitingFor`;
agent view is a research preview ([agent view](https://code.claude.com/docs/en/agent-view)).
Without a mod, a picker needs Claude to call `AskUserQuestion`, which costs a turn.

### Codex

Only built-in status line items exist, configured through `/statusline` in
`config.toml`, and there is no mod API. `codex "PROMPT"` starts with an initial
instruction ([developer commands](https://learn.chatgpt.com/docs/developer-commands?surface=cli)).
The carryover spec already excludes a Codex custom status line.

## lit data sources and measured cost

The local service was v0.7.5, measured 2026-10-14 with warm caches unless noted.

| Read | Gives | Cost |
| --- | --- | --- |
| `GET /v1/meta` (no client) | reachability, version, service ID, `title_prefixes` (for validating new project titles) | 4.5 ms |
| `GET /v1/completions/projects` (no client) | project titles and IDs for the picker | 9 ms / 100 items |
| `GET /v1/completions/issues` (no client) | issue titles, state, project | 43–59 ms warm, 330 ms cold / 100 items |
| `lit session get` | project, client ID, status (stage after §7 lands) | 16 ms process total |
| `lit claims list [--owner-client-id]` | held claims and expiry | 7 ms |
| `lit issues list --state open --blocked false --claimed false` | the ready frontier for an impl picker | 9 ms (limit 1) |
| `lit transactions` | uncertain mutations to reconcile | not measured |
| workflow-state `vendor-sessions/<sha256([host, runtime_id])>.json` | local association: session name, endpoint, project, status | file read, offline |

The `/v1` route list (`internal/service`, `internal/protocol`) has no change feed
or long-poll endpoint, so freshness comes from polling. At these costs a 5 s poll
from each open session is negligible. An aggregate endpoint (proposal S1) would
reduce this further.

Association evidence: 211 local associations, of which 199 are Claude `ready`
with a project and 10 are Codex. 107 runtime IDs are deliberately assigned names
(for example `coord-798a6f05-logis-go`), not vendor session IDs. Until hooks
register with `session_id` (spec §1), a mod's `$.session.id()` often finds no
association. That is the hard prerequisite.

## Proposed core UI

One mod, three sizes of the same state:

1. **Status entry** (`$.ui.status`): only when no lit status line is installed
   (open question 1). Otherwise the mod adds nothing there.
2. **Band** above the prompt: one line with state chips and one primary
   action button. It is dismissible.
3. **Pane** (`/lit-ui` or clicking the band): the full launcher and the board.

### State machine (what the band shows and offers)

| State | Detection | Band | Primary action |
| --- | --- | --- | --- |
| Offline | `/v1/meta` fails | `⚫ lit offline` | "How to start" (shows `lit-server` / systemd hint; never starts the server unasked) |
| Degraded | `503 recovery_required` | `⛔ lit needs recovery` | Opens docs/recovery.md guidance |
| Pending | association `status: pending` | `⏳ lit pending` | `lit workflow-session reconcile` (explicit click) |
| No project | session has no project | `○ no project` | Picker (below) |
| Stale preference | `lit context show --validate` fails | `⚠ remembered project unavailable` | Picker plus "Clear preference" |
| Ready | project selected | `🔥 feat/x · claims 2 (next expires 12m) · stage impl · 3 ready` | Recommended next stage |

### "No project" picker

- **Sections:** the remembered project for this checkout (validated), then
  projects whose `repository_refs` contain this repo's remote, then all others.
  Suggestions are sorted and labeled, never auto-selected (§9 rule 4).
- **Select existing:** `lit session set --project <id>`, with an optional "Remember
  for this checkout" toggle (`lit context set`).
- **Create new:** an `Input` for the title, validated live against
  `/v1/meta` `title_prefixes` and the title grammar, and a one-line goal. It then
  launches `/planner` with the title and goal. The planner creates the project, so
  there is one creation path and its human gate stays intact.

### Workflow launcher (project selected)

| Flow | Argument picker | Launch text |
| --- | --- | --- |
| planner | optional decision ticket from open `decision`-labelled issues | `/planner <project> [decision]` |
| to-spec | none, or the planning map | `/to-spec <project>` |
| to-tickets | spec or milestone | `/to-tickets <project> [milestone]` |
| impl | ready frontier (open, unblocked, unclaimed), oldest first, showing labels and blockers | `/impl <project>:<issue>` |
| orchestrate | milestone or multi-select tickets, plus an explicit scope-authorization step | `/orchestrate <project> milestone <m>` |

- **Recommended stage:** computed from tracker state and marked as the primary
  button. Open decisions point to planner. Settled decisions without a
  specification point to to-spec. A specification without implementation tickets
  points to to-tickets. A non-empty ready frontier points to impl, or to
  orchestrate when the frontier has 2 or more independent tickets. The heuristic
  is visible ("why?"), never automatic.
- **Launch mode:** default `$.prompt.fill`, which shows the exact command for one
  Enter. An opt-in "run immediately" uses `$.command.run`. Orchestrate always
  uses fill, because the skill requires an explicit execution scope.
- **Running stage:** while the session stage is set, the launcher shows it and
  warns before starting another outer stage in the same conversation.

### Errors surfaced

Errors shown: unreachable service, degraded mode, pending association,
service-ID mismatch, invalid remembered project, uncertain transactions to
reconcile, expired claims still believed held, client drafts, and recent `lit
hook` failures (spec §1 log). Each error offers its exact fix command.

## Further proposals

Ranked by value for cost. The mod items assume the core mod exists.

| # | Proposal | What it does | Data / mechanism | Cost |
| --- | --- | --- | --- | --- |
| P1 | **Lease clock** | Per-claim countdown chips, amber below 1/3 lease, red after expiry; a toast at 5 min left. | `claims list`, `$.clock.every`, `$.ui.toast` | S |
| P2 | **Event-driven refresh** | A `tool.call` hook on Bash commands starting with `lit ` refreshes after the call, so claims and stage update instantly instead of on the next poll. | `tool.call` after `next(e)` | S |
| P3 | **Human queue** | A badge counts issues labelled `needs-info`/`ready-for-human`, open decisions awaiting an answer, and handoffs. Clicking opens the issue or `/planner` on that decision. | `issues list --labels-any` | S |
| P4 | **Frontier board** pane | Columns Ready, Blocked (with blocker titles), Claimed (by whom, expiry), Done today. Enter on a ready card runs impl. | `issues list` filters, `claims list` | M |
| P5 | **Next-step suggestion** | When a stage clears (for example to-spec finished), show ghost text `/to-tickets feat/x`. | stage transition + `$.prompt.suggest` | S |
| P6 | **Session radar** | Every agent session on this project: lit session, stage, claims, and Claude runtime state from `claude agents --json`. Highlights "2 agents waiting for you" and links to `claude attach`. | `claims list` owners + session stages + `claude agents --json` (research preview) | M |
| P7 | **Planning-map progress** | Decisions settled vs open as a bar; the next unblocked question is a button. | decision issues and blockers under the map | M |
| P8 | **Milestone burn-up** | A sparkline of closed members over time per milestone, plus remaining frontier size. | milestone progress projection + `updated-after` | M |
| P9 | **Blocker graph widget** | A small DAG of the project with critical path highlighted; click a node to select it for impl or open it. | `Client` element with pointer events | L |
| P10 | **Explicit human claim actions** | "Renew" or "Release" buttons on the agent's claims, as deliberate human actions, never automatic. | `lit claims renew/release` | S, needs decision |
| P11 | **Checkout guard** | A soft toast when the agent edits files in a checkout whose mapped project issue another session has claimed, because claims do not fence files. | `tool.call` on Edit/Write + checkout mappings | M, risk of noise |
| P12 | **lit-tui launcher (Codex parity)** | An `L` action in lit-tui on a project or issue: choose host and flow, then suspend into `claude "/impl X"` or `codex "$impl X"` with the existing `tea.Exec` path (`internal/tui/suspend.go`). No lease supervision, so it stays outside the deferred launcher idea. | lit-tui | M |
| S1 | **Server digest endpoint** | `GET /v1/digest?project=` returns counts (open/ready/blocked/claimed), this client's claims with expiry, open decisions, uncertain transactions and a `state_token` with ETag/304. That makes one call per refresh for status line, mod and TUI. | new read route | M |
| S2 | **Change long-poll** | `GET /v1/changes?after=<token>` blocks until something in scope changes, making the pane push-like with zero idle polling. | needs a global change sequence | L |

Performance budget: the mod keeps one cached snapshot per session (`$.state`),
polled every 5–10 s while visible and every 30 s while hidden, plus event
refresh (P2). Every `$.process.run` gets a timeout of 2 s or less and a failure
backoff. The status line stays on the spec's cache and is never blocked by the
mod.

## Open questions for the human

1. **Status line vs mod.** Keep the specified `lit hook statusline` and have
   the mod add only band and pane, or let the mod replace lit's status part when
   loaded? Recommendation: keep the status line as the cross-environment baseline
   and make the mod additive.
2. **Launch semantics.** Is fill-then-Enter the default, or immediate run?
   Recommendation: fill by default and immediate opt-in, never for orchestrate.
3. **Human claim buttons (P10).** Are explicit human renew/release clicks
   acceptable given "hooks never renew"? They are human actions, not hooks, but
   they touch an agent's ownership.
4. **Project creation.** Is it planner-only (proposed), or should the mod create
   the project directly and then start the planner?
5. **Distribution.** Ship the mod from this repository and install it with
   `lit setup-workflows` (next to the `lit-hooks` plugin), or through a plugin
   marketplace?
6. **Server work.** Is S1 worth it now, or later once polling cost is observed?

## Human decisions (2026-10-14)

The six open questions above were answered:

1. The mod replaces the lit status line, and users can define their own.
2. Launches fill the prompt by default, for now.
3. Human renew/release buttons are acceptable.
4. The mod may create projects directly.
5. The mod ships through `lit setup-workflows`.
6. The digest endpoint belongs to this effort but is deferred while research
   continues.

On 2026-10-16 decision 1 was refined: the status surface is chosen at install
time with `lit setup-workflows --status line|mod|both`. `line` keeps the status
line already specified on `feat/workflow-hooks`; `mod` installs only the mod and
restores a lit-managed status line to its recorded original (or removes lit's
own); `both` installs both. The default and the Codex meaning are still open
(issue "setup-workflows --status line|mod|both").

Research, decisions, prototypes, findings and ideas are tracked in the lit
project `research/lit-agent-ui`.

## Prototype results (2026-10-14)

All prototype artifacts are under
`~/.local/share/coding-artifacts/lit/prototype-agent-ui/`, outside version
control:

- `mods/lit-ui/`: the prototype mod. `claude plugin validate` and `tsc` are
  clean, and `claude plugin test` runs 8 passing tests.
- `mods/lit-probe/`: an offline probe. It answers every model request locally,
  so nothing is billed.
- A seeded lit-server on 127.0.0.1:7499, with its own `LIT_STATE_DIR` and
  `LIT_WORKFLOW_STATE_DIR`.

Both mods ran in a private tmux server through `claude --plugin-dir`, so no
other Claude session or the real tracker was touched.

### Answered questions

- **Skill launch.** `$.command.list()` lists project skills with source `user`.
  `$.prompt.fill('/planner …')` followed by Enter expands the skill. So does
  `$.command.run({command, args})` called from a timer: the prompt carries
  origin `plugin`. The same call made inside a `command.run` hook is refused
  ("it would wait on the turn this hook is holding").
- **Session identity.** `$.session.id()` equals the hook and status line
  `session_id` at startup and after resume. After `/clear` the ID changes and
  no `session.start` follows, so the mod must re-read the ID on every refresh.
- **Band hotkeys.** A bare digit typed in an empty prompt presses a band
  button. `1` started the session, opened the pane, and filled the
  recommended launch.
- **Cost.** These were measured inside the mod:

  | Call | Time |
  | --- | --- |
  | Spawn a host process | 8–40 ms |
  | `lit version` | 32–48 ms |
  | `$.http.fetch` of `/v1/meta` | 7–25 ms |
  | Full refresh (about 7 spawns) | 96–417 ms |

  `lit workflow-session … run` costs 70–590 ms and holds a per-runtime lock.
  The prototype therefore reads the association file directly and calls the
  CLI with `--session`. One HTTP digest would replace most of the spawns.

### Observed live

These features were seen working:

- band states;
- project picker;
- direct creation with live title validation;
- launcher preview and fill;
- stage tracking from `skill.prompt`;
- next-step ghost text;
- lease toast, renew from the band, and the countdown;
- waiting queue, board, critical path and milestones;
- the per-session feed file, read by a user-defined status line script.

### Not established

- **Event-driven refresh and the checkout guard.** A stubbed model step that
  lists `toolUses` does not make Claude Code run the tool, so these need one
  real turn or a test harness.
- **Mouse interaction and the desktop surface.**
- **Session radar.** It does not yet use `claude agents --json`.
- **Graph widget.** Only a text critical path exists, not the `Client` widget.
- **Remembered project.** `lit context` is not implemented yet.
- **lit-tui launcher.** Not built.

### Findings for lit

- `workflow-session run` is too slow and lock-contended for a polling UI.
- The CLI writes `.lit/<client>/state.json` into its working directory.
- Issue projections show every owner's claim token.
- The next-step suggestion needs the server-side stage clear (carryover spec
  §7). A stage spans many turns.

## Sources

- Tracker: project `feat/workflow-pocket-carryover` (revision 3, read
  2026-10-14), [decision 36155bd6] and its resolution comment `0d7fed39`.
- Code at commit 3f2aed0: `internal/workflowsession/session.go` (association key,
  state paths), `internal/protocol/protocol.go` (`Meta`),
  `docs/completion-api.md`, `internal/tui/suspend.go`, `docs/ideas/agent-launcher.md`,
  `docs/research/agent-liveness.md`, skill sources under
  `internal/workflow-skills/skills/`.
- CLI help: `lit --help`, `lit issues list --help`, `lit claims list --help`.
- Claude Code 2.1.296 bundled `plugin-authoring` skill and its generated
  `claude-code.d.ts` (`$.command.run`, `$.prompt.*`, `$.process.run`,
  `$.session.id`, `Elements`, `SelectProps`, `ClientProps`, `CommandSource`).
- Web documentation listed inline, read 2026-10-14.

[decision 36155bd6]: lit:issue:36155bd6-8e63-43d4-9142-1bf1d6d29add
