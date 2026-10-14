---
name: clarify
description: Interview the user to settle a plan or decision, recording accepted answers and warranted domain documentation as the discussion proceeds.
---

Workflow stage: when the user invokes this skill directly, record it with
`lit session set --stage clarify` before starting. When another workflow skill
invokes it, leave the stage unchanged. On completion or handoff, run
`lit session unset --stage`. The Workflow stage section of the
[lit skill](../lit/SKILL.md) covers the session and older lit versions.

Build a shared understanding of the decision, its constraints, and the questions
that depend on it. Read applicable repository instructions and domain docs first.
Use [modeling](../modeling/SKILL.md) by default when terms or relationships change;
it does not require creating a glossary or ADR for every conversation.

Investigate discoverable facts from the actual artifacts rather than asking the
user to supply them. Separate observed behavior, proposals, assumptions, and
accepted decisions. A code/decision mismatch is evidence to discuss, not authority
to overwrite the user's decision. Delegate bounded fact gathering when useful
and supported by the host; pending evidence blocks only dependent questions.

Ask currently answerable questions in small rounds, usually two to four related,
independently answerable questions. One question is fine when only one is ready;
do not pad a round or dump the entire decision tree. In planner work, stay within
the active decision ticket. Wait for answers or pending evidence before asking
questions that depend on them.

Use this presentation by default, rather than burying questions in free-form
prose. Keep question numbers unique across the discussion and retain the same
labels when returning to unanswered questions:

```markdown
❓ **Q1 — <short title>**
<The question, with enough context and tradeoffs to answer. Use a short list
of named options when useful.>

➡️ **Recommendation:** <Suggested answer and a brief, grounded reason.>

---

❓ **Q2 — <short title>**
<The next independently answerable question.>

➡️ **Recommendation:** <Suggested answer and a brief, grounded reason.>
```

Recommend only when evidence or the user's constraints support it. Otherwise
use `➡️ **Open choice:**` and explain the tradeoff or missing information without
inventing a preferred answer. Investigate discoverable facts yourself; do not
turn settled answers or factual findings into redundant approval questions.
Keep introductions short and put the relevant explanation with its question.

If the host requires a native question tool, use that interface, retaining the
question labels and recommendation/reason where supported; do not duplicate the
questions in prose. Explicit user preferences override this default format.

Accept partial answers. Record explicit decisions, retain unanswered labels, and
recompute the remaining questions before the next round. Reask only questions
that still matter; do not treat silence as agreement or demand answers to branches
made irrelevant by earlier decisions.

Record accepted answers during the discussion, before moving to the next round.
Use the existing decision record or shared tracker discussion when one is in
scope. For a standalone conversation, use the agreed repository-local discussion
file (or a small Markdown decision note when no destination exists); no running
tracker is required. Capture the answer, rationale, unresolved points, and evidence
links without presenting your recommendation as accepted. If the user requests
read-only discussion, retain a proposed note in the response instead of writing.
Application-owned workflow notes use `schema_version: 1` front matter; do not add
format markers to user-owned prose or existing repository domain documents.

Shared project decisions belong in the tracker when working in that workflow;
repository-specific glossary/ADR changes belong in each owning repository. Read
that repository's rules and resolve its paths from its own root. Reuse the lit
skill's ownership policy for tracked work; do not demand a tracker connection for
an unrelated decision conversation. If an in-scope tracker write fails, preserve
pending notes locally and report the missing update without claiming it succeeded.

At the end, summarize settled decisions and remaining uncertainties and obtain
confirmation of shared understanding. This closes the design discussion; it does
not authorize implementation. Existing implementation authorization remains valid
within its scope. Use [challenge](../challenge/SKILL.md) when deliberate scrutiny
would help, without silently reopening settled choices.
