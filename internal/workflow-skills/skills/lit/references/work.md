# Find, claim, and read

Use `lit issues list`, `lit grep`, `lit search`, and `lit claims` for discovery and
ownership. Check each command family's `--help` for filters and input syntax.
`lit search` ranks words by relevance (titles first, then bodies, then comments;
diacritics ignored); use it to find work by topic, and
`lit grep` for literal substrings such as identifiers, paths and error messages,
or `lit grep -E` for regular expressions.

For user-named work, read its project, issue, comments, blockers, shared spec,
and current ownership. Without a named implementation ticket, propose an open,
unblocked, unclaimed ready-for-agent candidate, oldest first, and obtain selection
confirmation. Do not substitute another candidate after losing a claim race.

Acquire ownership atomically before sustained work, then reread current state.
Use server-side structured discovery, grep and ranked search; fetch matching
records and comments and follow relationships. Search excerpts are discovery aids,
not authority for old revisions. Follow every continuation cursor needed for the
question. Keep all-project searches explicit.

Record repository identities and roles in the project; keep checkout paths local.
A ticket can span repositories. Missing access blocks dependent work, not unrelated
progress. Renew claims explicitly during authorized work before expiry. An expired
claim must be reacquired, and current state reread; no waiting grace or force.
