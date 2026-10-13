# Checkpoint, recover, finish

Check executable help for available comment, claim, closure, and transaction-status
operations. Do not invent absent commands.

At meaningful milestones, commit ticket-owned changes first, then record full
hashes, repository/branch identities, checks/results, remaining steps, and current
TDD phase on the ticket. Preserve referenced commits. Git and lit are separate
systems; record partial successes rather than claiming atomic completion.

On an orderly interruption, preserve unfinished work with an explicit WIP checkpoint
and handoff, recording expected failures; release ownership when the service is
available. Failed checks/hooks/commits leave work open. Do not push automatically.

If lit becomes unavailable, stop new claimed implementation work, preserve local
changes and a checkpoint/handoff, and report pending tracker updates. A timeout can
hide a committed mutation. Use the CLI's retained request identity and supported
status reconciliation before replaying anything. Never delete pending state or
create a new logical client to bypass uncertainty. The host adapter's `reconcile`
only adopts a confirmed existing connection; it does not replay mutations.

Some uncertain outcomes cannot resolve through repeated status checks. A lost
first-connect response may leave no client identity
(`registration_identity_not_received`); absent history plus changed original
preconditions may produce `original_preconditions_no_longer_valid`. Exit 4 can
persist indefinitely. There is no supported registration-identity repair or
unresolved-evidence retirement command.
Preserve the exact pending evidence and proof, stop dependent mutations, and
escalate to the human with confirmed facts and remaining uncertainty in a durable
handoff. Do not interpret human escalation as permission to delete/move evidence,
replay requests, or create another client to bypass the outcome. If accepted
history cannot establish the result, a repair design or maintainer assistance
is needed; this skill provides no manual retirement procedure.

After reconnecting, reconcile uncertain outcomes, reacquire expired ownership, and
reread issue/comments/relationships before resuming. Do not use stale claim tokens
or assume reconnect extends the lease. If another client owns the work, stop and
report the conflict; agents never force takeover.

For implementation tickets, finish only after acceptance criteria and required
checks pass, relevant changes are committed, and commit/check evidence is recorded
on the ticket. Close afterward; closure ends the claim. If final evidence or
closure is interrupted, establish which operations committed before retrying.
Preserve outstanding steps in the handoff.
