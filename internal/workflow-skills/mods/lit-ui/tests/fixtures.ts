// Shared test data: the seeded harness project in miniature.
import type { Claim, Issue, Raw } from '../types'

export const NOW = Date.parse('2026-11-20T00:00:00Z')
let created = 0
export const issue = (id: string, title: string, extra: Partial<Issue> = {}): Issue => ({
  id, title, state: 'open', labels: [], blocked_by: [], claim: null,
  created_at: new Date(NOW - 86400000 + ++created * 1000).toISOString(), ...extra,
})
export const claim = (issue_id: string, owner: string, minutes: number): Claim =>
  ({ issue_id, owner_client_id: owner, owner_name: owner, expires_at: new Date(NOW + minutes * 60000).toISOString() })

// The seeded harness project in miniature: one open decision, one ready ticket,
// two blocked, one claimed by another agent, one needs-info and one ready-for-human.
export function seed(overrides: Partial<Raw> = {}): Raw {
  const issues = [
    issue('d1', 'Which payment provider?', { labels: ['decision'] }),
    issue('d2', 'Rounding?', { labels: ['decision'], state: 'closed' }),
    issue('t1', 'Add cart model', { labels: ['ready-for-agent'] }),
    issue('t2', 'Add payment API client', { labels: ['ready-for-agent'], blocked_by: ['d1'] }),
    issue('t3', 'Checkout endpoint', { labels: ['ready-for-agent'], blocked_by: ['t1', 't2'] }),
    issue('t4', 'Receipt email', { labels: ['ready-for-agent'], claim: claim('t4', 'other', 50) }),
    issue('t0', 'Scaffold', { labels: ['ready-for-agent'], state: 'closed' }),
    issue('q1', 'Refund flow unclear', { labels: ['needs-info'] }),
    issue('q2', 'Review copy', { labels: ['ready-for-human'] }),
    issue('i1', 'Idea: dark mode', { labels: ['idea'] }),
  ]
  return {
    sid: 's1', endpoint: 'http://127.0.0.1:7411', reachable: true, degraded: false, prefixes: ['feat', 'fix'],
    assoc: { local_session: 'agent-1', endpoint: 'http://127.0.0.1:7411', client_id: 'me', project_id: 'p1', status: 'ready' },
    projectId: 'p1', stage: null, stageKnown: true,
    projects: [{ id: 'p1', title: 'feat/demo', description: '# Demo\n\n## Shared specification\n\nText.', repository_refs: [] }],
    issues, claims: [], otherTitles: {}, milestones: [], errors: [], ...overrides,
  }
}
