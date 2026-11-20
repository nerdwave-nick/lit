// The lit-ui mod's state contract and the data shapes it holds. Self-contained by rule:
// no imports. Claim tokens never appear in any of these shapes.

export type StatusMode = 'mod' | 'both'

// What setup records for the mod (config.json beside plugin.json), or the dev default.
export type Config = {
  mode: StatusMode
  // The lit executable: an absolute path from setup, or 'lit' from PATH.
  lit: string
  source: 'file' | 'default'
  // Why a config.json present was not used.
  problem: string | null
}

export type Claim = {
  issue_id: string
  expires_at: string
  owner_client_id: string
  owner_name: string | null
}
export type Issue = {
  id: string
  title: string
  state: 'open' | 'closed'
  labels: string[]
  blocked_by: string[]
  claim: Claim | null
  created_at: string
}
export type Project = { id: string; title: string; description: string; repository_refs: string[] }
export type Milestone = { id: string; title: string; issue_ids: string[]; closed: number; total: number }
// The fields of a workflow-session association the mod uses.
export type Assoc = {
  local_session: string
  endpoint: string
  client_id: string | null
  project_id: string | null
  status: string
}

// One refresh's reads, before derivation.
export type Raw = {
  sid: string
  // The endpoint probed: the association's, else the one a connect would use.
  endpoint: string
  reachable: boolean
  degraded: boolean
  prefixes: string[]
  assoc: Assoc | null
  projectId: string | null
  stage: string | null
  // False when `session get` failed: the stage is then unknown, not cleared.
  stageKnown: boolean
  projects: Project[]
  issues: Issue[]
  // This session's own claims; null when they could not be read.
  claims: Claim[] | null
  // Titles of claimed issues outside the selected project, by ID.
  otherTitles: Record<string, string>
  milestones: Milestone[]
  errors: string[]
}

export type Flow = 'planner' | 'to-spec' | 'to-tickets' | 'impl' | 'orchestrate'
export type State = 'offline' | 'degraded' | 'nosession' | 'pending' | 'noproject' | 'ready'
export type Recommendation = {
  flow: Flow | null
  reason: string
  // The decision, ticket or milestone title the command targets.
  arg: string | null
  // The exact text `1` puts in the prompt.
  command: string | null
}
export type ClaimView = { issueId: string; title: string; expiresAt: number; remainingMs: number; owner: string }
export type Blocked = { issue: Issue; by: string[] }
export type WaitingKind = 'decision' | 'needs-info' | 'ready-for-human'
export type Waiting = { issue: Issue; kind: WaitingKind; command: string }

export type View = {
  state: State
  sid: string
  endpoint: string
  project: Project | null
  stage: string | null
  ownClaims: ClaimView[]
  othersClaims: ClaimView[]
  // Work tickets: open issues labelled ready-for-agent.
  work: Issue[]
  ready: Issue[]
  mine: Issue[]
  blocked: Blocked[]
  waiting: Waiting[]
  openDecisions: Issue[]
  settledDecisions: number
  hasSpec: boolean
  recommend: Recommendation
  milestones: Milestone[]
  projects: Project[]
  prefixes: string[]
  errors: string[]
}

// What /lit-ui debug reports about the last refresh.
export type RefreshInfo = { at: string; ms: number; session: string | null }

declare module 'claude-code' {
  interface PluginState {
    'lit-ui': { view: View | null; refresh: RefreshInfo | null }
  }
}
