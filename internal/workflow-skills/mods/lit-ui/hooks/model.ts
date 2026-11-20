// The derived view: states, work, waiting items and the recommended next step, as the
// shared specification defines them. Pure (no `$`), so it is unit-tested.
import type { Claim, ClaimView, Flow, Issue, Milestone, Raw, Recommendation, State, View, Waiting } from '../types'

const WAITING_LABELS = ['needs-info', 'ready-for-human'] as const
export const has = (i: Issue, label: string) => i.labels.includes(label)
const isDecision = (i: Issue) => has(i, 'decision')
const isWaitingLabel = (i: Issue) => WAITING_LABELS.some(l => has(i, l))
// Only triaged tickets are work: ideas, findings, research and decisions never count.
export const isWorkKind = (i: Issue) => has(i, 'ready-for-agent') && !isDecision(i) && !isWaitingLabel(i)
const isWork = (i: Issue) => i.state === 'open' && isWorkKind(i)
const byCreated = (a: Issue, b: Issue) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id)
const SPEC_HEADING = /^#{1,6}[ \t]+Shared specification/m

export function stateOf(raw: Raw): State {
  if (!raw.reachable) return 'offline'
  if (raw.degraded) return 'degraded'
  if (!raw.assoc) return 'nosession'
  if (raw.assoc.status !== 'ready') return 'pending'
  return raw.projects.some(p => p.id === raw.projectId) ? 'ready' : 'noproject'
}

export function derive(raw: Raw, now: number): View {
  const state = stateOf(raw)
  const project = state === 'ready' ? raw.projects.find(p => p.id === raw.projectId) ?? null : null
  const issues = project ? raw.issues : []
  const me = raw.assoc?.client_id ?? null
  const byId = new Map(issues.map(i => [i.id, i]))
  // A blocker outside the project is unknown here and counts as open.
  const openBlockers = (i: Issue) => i.blocked_by.filter(b => byId.get(b)?.state !== 'closed')
  const open = issues.filter(i => i.state === 'open').sort(byCreated)
  const work = open.filter(isWork)
  const claimView = (c: Claim): ClaimView => {
    const expiresAt = Date.parse(c.expires_at)
    return {
      issueId: c.issue_id,
      title: byId.get(c.issue_id)?.title ?? raw.otherTitles[c.issue_id] ?? c.issue_id.slice(0, 8),
      expiresAt,
      remainingMs: expiresAt - now,
      owner: c.owner_name ?? c.owner_client_id.slice(0, 8),
    }
  }
  const openDecisions = open.filter(isDecision)
  const title = project?.title ?? ''
  const waiting: Waiting[] = [
    ...openDecisions.map(i => ({ issue: i, kind: 'decision' as const, command: launchText('planner', title, i.title) })),
    ...open.filter(isWaitingLabel).map(i => has(i, 'needs-info')
      ? { issue: i, kind: 'needs-info' as const, command: `/triage ${title}:${i.title}` }
      : { issue: i, kind: 'ready-for-human' as const, command: `Show me the lit issue "${i.title}" (${i.id.slice(0, 8)}) in ${title} and what it needs from me.` }),
  ]
  const view: View = {
    state,
    sid: raw.sid,
    endpoint: raw.endpoint,
    project,
    stage: raw.stage,
    ownClaims: (raw.claims ?? []).map(claimView).sort((a, b) => a.remainingMs - b.remainingMs),
    othersClaims: work.filter(i => i.claim && i.claim.owner_client_id !== me).map(i => claimView(i.claim!)),
    work,
    ready: work.filter(i => !i.claim && openBlockers(i).length === 0),
    // The claim expiring first leads, as the band shows it.
    mine: me ? work.filter(i => i.claim?.owner_client_id === me).sort((a, b) => Date.parse(a.claim!.expires_at) - Date.parse(b.claim!.expires_at)) : [],
    blocked: work.filter(i => openBlockers(i).length > 0)
      .map(i => ({ issue: i, by: openBlockers(i).map(b => byId.get(b)?.title ?? b.slice(0, 8)) })),
    waiting,
    openDecisions,
    settledDecisions: issues.filter(i => isDecision(i) && i.state === 'closed').length,
    hasSpec: SPEC_HEADING.test(project?.description ?? ''),
    recommend: { flow: null, reason: '', arg: null, command: null },
    milestones: raw.milestones,
    issues,
    projects: raw.projects,
    origin: raw.origin,
    prefixes: raw.prefixes,
    errors: raw.errors,
  }
  if (project) view.recommend = recommend(view, issues.length)
  return view
}

// The recommended next step: the first rule that matches wins.
export function recommend(v: View, issueCount: number): Recommendation {
  const p = v.project!.title
  const to = (flow: Flow, reason: string, arg: string | null = null): Recommendation =>
    ({ flow, reason, arg, command: launchText(flow, p, arg) })
  if (issueCount === 0) return to('planner', 'The project has no issues yet: start planning.')
  if (v.mine.length > 0) return to('impl', 'You hold the claim on this ticket.', v.mine[0]!.title)
  const decisions = v.openDecisions.length
  if (decisions > 0) return to('planner', `${decisions} open ${decisions === 1 ? 'decision' : 'decisions'} to settle.`, v.openDecisions[0]!.title)
  if (!v.hasSpec && v.settledDecisions > 0) return to('to-spec', 'Decisions are settled and there is no shared specification yet.')
  if (v.hasSpec && v.work.length === 0) return to('to-tickets', 'The specification has no open implementation tickets.')
  if (v.ready.length >= 2) return to('orchestrate', `${v.ready.length} tickets are ready.`, busiestMilestone(v.milestones, v.ready))
  if (v.ready.length === 1) return to('impl', 'One ticket is ready.', v.ready[0]!.title)
  const parts = [v.blocked.length && `${v.blocked.length} blocked`, v.othersClaims.length && `${v.othersClaims.length} claimed by others`].filter(Boolean)
  return { flow: null, reason: parts.length ? `Nothing to start: ${parts.join(', ')}.` : 'Nothing to start.', arg: null, command: null }
}

// The milestone containing most ready tickets (the first on a tie), or null for none.
function busiestMilestone(milestones: Milestone[], ready: Issue[]): string | null {
  const ids = new Set(ready.map(i => i.id))
  const count = (m: Milestone) => m.issue_ids.filter(id => ids.has(id)).length
  let best: Milestone | null = null
  for (const m of milestones) if (count(m) > 0 && (!best || count(m) > count(best))) best = m
  return best?.title ?? null
}

export function launchText(flow: Flow, project: string, arg: string | null): string {
  switch (flow) {
    case 'planner': return `/planner ${project}${arg ? ` decision: ${arg}` : ''}`
    case 'to-spec': return `/to-spec ${project}`
    case 'to-tickets': return `/to-tickets ${project}${arg ? ` milestone ${arg}` : ''}`
    case 'impl': return `/impl ${project}:${arg ?? ''}`
    case 'orchestrate': return `/orchestrate ${project}${arg ? ` milestone ${arg}` : ''}`
  }
}

export function duration(ms: number): string {
  if (ms <= 0) return 'expired'
  const m = Math.floor(ms / 60000)
  if (m >= 60) return `${Math.floor(m / 60)}h${m % 60}m`
  return m > 0 ? `${m}m` : `${Math.ceil(ms / 1000)}s`
}
