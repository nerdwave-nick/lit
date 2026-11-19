# lit UI in Claude Code: interaction and visual design

Status: accepted (2026-10-16). Model for the `lit-ui` prototype mod; see
`lit-workflow-ui.md` for research and decisions. The clickable design is the
artifact canvas https://claude.ai/artifact/Vc44xeTwEwpj6uabpEpzUP (private).

## What is wrong with the first prototype

Observed in a live session (screenshot 2026-10-16):

1. **No hierarchy.** Seven bold headings of equal weight, no spacing, one long
   column. Nothing says "do this next".
2. **Everything at once.** Project switcher, project creation, launcher, claims,
   queue, board and debug footer all render, including empty ones (`none`,
   `ready 0 · blocked 0 · claimed 0 · done 0`).
3. **Cryptic controls.** `switch:`, `new:`, `flow:` dropdowns; a legend for `★`
   that appears nowhere; `[ f fill prompt ][ r run now ]` with no hint that the
   letters only work after focusing the pane.
4. **No way out shown.** Closing (`Esc`, `ctrl+x x`, `×`) and collapsing (`[-]`)
   are engine controls the UI never mentions.
5. **One action, four rows.** Recommendation text, flow dropdown, command preview
   and two buttons all express "start /to-spec".
6. **Debug in the product.** Session ID and refresh time in the footer.
7. **Dropdowns steal focus.** A `Select` pick returns focus to the prompt, so the
   next hotkey goes nowhere.

## Principles

1. **Look like Claude Code.** Reuse the host's dialog idiom: a framed panel, a
   bold title, a pointer list (`❯`), dim secondary text, a dim key-hint footer
   (`Enter to confirm · Esc to close`). Nothing that looks like a web form.
2. **One next step.** Every view leads with at most one primary action, drawn in
   the accent colour. Everything else is secondary and dim until focused.
3. **Show only what is non-empty.** Empty sections disappear; zero counts are not
   printed. The quiet state is a short line, not a dashboard of zeros.
4. **Lists, not dropdowns.** Choices are numbered rows pressed by digit or Enter
   (Buttons with hotkeys), so focus never jumps back to the prompt.
5. **Views instead of a scroll.** The pane has four small views (Home, Launch,
   Board, Project); each fits on one screen.
6. **Leave when done.** Launching puts the command in the prompt and closes the
   pane, so the cursor is where the next keystroke (Enter) belongs.
7. **Colour means state.** Green ready, yellow attention, red error, dim meta,
   accent only for the primary action. Every state also has a glyph.

## Vocabulary

| Glyph | Meaning | Colour |
| --- | --- | --- |
| `🔥` | connected, project selected | — |
| `○` | not set up (no session or no project) | warning |
| `⚫` | tracker not answering | error |
| `◷` | my claim, with time left | normal; warning under 5 min |
| `⚑` | waiting for a human (decision, needs-info, ready-for-human) | warning |
| `●` / `◌` / `◐` / `✓` | ready / blocked / claimed by others / done | success / dim / warning / dim |
| `❯` | the focused row | accent |

## The band (one row above the prompt)

The band answers "where am I, and what is the one thing to do?". It shows only
facts that need attention; zero counts and `idle` are omitted. Buttons are drawn
`plain` (`1:label`) to avoid bracket clutter. The engine's `[-]` stays at the end.

```
🔥 research/lit-agent-ui · ⚑ 1 waiting                      1:to-spec  2:lit  [-]
🔥 feat/demo-checkout · ◷ Add cart model 24m · impl         1:/impl …  2:lit  [-]
🔥 feat/demo-checkout · ◷ Add cart model 3m left            3:renew    2:lit  [-]
○ lit · no project selected                                1:choose project  [-]
○ lit · not connected in this conversation                 1:connect         [-]
⚫ lit · tracker not answering (127.0.0.1:7411)             1:retry           [-]
```

- The project name is the only bold text. A claim expiring under 5 minutes turns
  the row yellow and replaces the launch button with `3:renew`.
- While a turn runs (`isWorking`), action buttons hide; the facts stay.
- With `--status both` the status line already shows project and claims; the
  band then drops them and keeps only attention items and actions:
  `⚑ 1 waiting · next /to-spec   1:to-spec  2:lit`.

## The pane

Opened by `2` from an empty prompt, `/lit`, or a click on the band. It opens
focused and with `closeOnEscape`, so **Esc closes it**. The frame and `×` are the
engine's. Inline it sits above the prompt; docked (fullscreen, ≥110 columns) it
sits beside the transcript at roughly 48 columns, where right-hand meta drops.

Every view has the same skeleton:

```
 lit · <project>                              Home  Launch  Board  Project
                                                   (h, l, b, p switch views)
 <view body>

 <key hints, dim>
```

The view tabs are plain Buttons with letter hotkeys; the current one is bold and
underlined, the others dim.

### Home (default)

The next step, then my claims, then what waits for me. Nothing else.

```
 lit · research/lit-agent-ui                  Home  Launch  Board  Project

 NEXT STEP
 ❯ /to-spec research/lit-agent-ui
   Decisions are settled and there is no shared specification yet.

 WAITING FOR YOU
   1  ⚑ Review the lit-ui prototype and decide what to keep   ready-for-human

 Enter put in prompt · r run now · l other workflow · Esc close
```

With claims and work:

```
 lit · feat/demo-checkout                     Home  Launch  Board  Project

 NEXT STEP
 ❯ /impl feat/demo-checkout:Add cart model
   1 ticket ready; you hold its claim.

 MY CLAIMS
   1  ◷ Add cart model                          3m left    r renew   x release

 WAITING FOR YOU
   2  ⚑ Which payment provider?                 decision
   3  ⚑ Refund flow unclear                     needs-info

 Enter put in prompt · 1-3 select · Esc close
```

- Pressing a waiting item's digit puts the matching command in the prompt
  (`/planner <project> decision: <title>` for a decision; `lit issues show` for
  the rest) and closes the pane.
- A selected claim row exposes `r renew` and `x release`; release asks once
  (`x again to release`).

### Launch

The five workflows as a numbered list. Unavailable ones stay visible, dim, with
the reason, so the list never changes shape.

```
 lit · feat/demo-checkout                     Home  Launch  Board  Project

 START A WORKFLOW
   1  planner       settle open decisions          ⚑ 1 open
   2  to-spec       write the shared specification
   3  to-tickets    cut implementation tickets     needs a spec
 ❯ 4  impl          implement one ready ticket     ★ 1 ready
   5  orchestrate   run ready tickets in parallel  needs 2+ ready

 Enter choose · Esc back
```

Choosing a workflow that needs a target opens a second step in the same view:

```
 IMPLEMENT WHICH TICKET?
 ❯ 1  ● Add cart model
   2  ◌ Checkout endpoint            blocked by Add cart model

 → /impl feat/demo-checkout:Add cart model

 Enter put in prompt · r run now · Esc back
```

Targets: impl → ready tickets (blocked shown dim, not choosable); planner →
open decisions, plus "whole map"; to-tickets / orchestrate → milestones, plus
"whole project". The exact command is always shown once, on the `→` line.

### Board

Read-only overview; rows are not actions.

```
 lit · feat/demo-checkout                     Home  Launch  Board  Project

 1 ready · 2 blocked · 1 claimed · 1 done

 ●  Add cart model
 ◌  Checkout endpoint             ← Add cart model, Add payment API client
 ◌  Add payment API client        ← Which payment provider?
 ◐  Receipt email                 seed-agent · 50m

 LONGEST CHAIN
    Which payment provider? → Add payment API client → Checkout endpoint

 M1 MVP   ████████░░░░░░░░  2/5

 Esc close
```

### Project

Also the view the pane opens on when no project is selected.

```
 lit · no project                              Home  Launch  Board  Project

 CHOOSE A PROJECT
 ❯ 1  research/lit-agent-ui              current
   2  feat/workflow-pocket-carryover     this repository
   3  feat/lit-sqlite-storage            this repository
   4  more…                              12 other projects

 NEW PROJECT
   n  feat/█                             prefix: feat fix docs research …
      creates it, selects it and puts /planner in the prompt

 Enter select · n new · Esc close
```

- At most the current project plus this repository's projects as numbered rows;
  `more…` opens a filterable list (Input + rows) in the same view.
- Title validation shows inline under the input, in red, only after typing.

## Errors and notices

- A failure shows once as a red row at the top of the current view
  (`⚠ renew failed: claim expired`) and as a toast; it clears on the next
  successful refresh.
- Session ID, refresh timing and benchmarks move to `/lit-ui debug`.
- Toasts only for events the person did not cause: a claim about to expire, a
  decision answered by someone else, the tracker going offline.

## Keys (summary)

| Where | Key | Does |
| --- | --- | --- |
| empty prompt | `1`–`3` | press the band's buttons |
| anywhere | `ctrl+x tab` | focus band / pane |
| anywhere | `ctrl+x ctrl+a` / `[-]` | collapse the band |
| pane | `h l b p` | switch view |
| pane | digits, `↑↓`, Enter | choose a row / primary action |
| pane | `r` | run now instead of putting in the prompt |
| pane | Esc | back one step, then close |
| pane | `ctrl+x x` / `×` | close |

## Feasibility on the mod API (Claude Code 2.1.296)

| Need | Element or call |
| --- | --- |
| framed panel, title, close mark | `Pane` (engine frame, `×`, `ctrl+x x`) |
| Esc closes | `$.ui.open({ id, focus: true, closeOnEscape: true })` |
| numbered rows, view tabs | `Button` with `hotkey`, `plain`, `variant: 'primary'` |
| focused row highlight | Button focus ring; `Box hover` for pointer |
| section labels, dim meta, colours | `Text` (`bold`, `dimColor`, `color`: theme keys) |
| right-aligned meta | `Box justifyContent="space-between"`; drop when `bodyColumns` < 60 |
| progress bars | `Text` blocks (`█░`) |
| project filter, new title | `Input` |
| close after launch | `$.ui.close({ id })` after `$.prompt.fill` |
| band width | `AbovePrompt.bodyColumns`, `isWorking` |

Not yet verified: whether `Esc` from a nested step can be intercepted to go back
before closing (a `ui.close` hook answering without `next` keeps the pane open,
which may be enough), and how the focus ring draws on a plain Button.

## Decisions (2026-10-16)

The human accepted the design as drawn on the canvas ("The design looks awesome
this way, yes. Let's go with that"), which settles the four open questions:

1. Four views with tabs (Home, Launch, Board, Project).
2. With `--status both` the band drops the project and claim (unless it is
   expiring) and shows `next /<flow>` instead.
3. Launching puts the command in the prompt and closes the pane.
4. `r run now` stays as the secondary action.

## Implementation notes (prototype mod, Claude Code 2.1.296)

Verified in a private tmux session against the isolated tracker:

- `Button` `autoFocus` only applies when the site takes the keyboard, and a
  hotkey press does not move the focus ring. The mod calls
  `$.ui.focus({ requestId, key })` after each choice so Enter acts on
  "put in prompt" (target step) or the next step (Home).
- `n` focuses the new-project `Input` the same way.
- `$.ui.open` must run synchronously inside the press: after an `await` the
  engine no longer treats it as the person's request and below 144 columns
  leaves the pane unplaced.
- Esc in a target step goes back: a `ui.close` hook answers `{ value: undefined }`
  without `next` while a step is open.
- The validator follows `$` only within one file, so CLI reads in `gather.ts`
  go through an `Io` object built from `$` in `register.tsx`.
