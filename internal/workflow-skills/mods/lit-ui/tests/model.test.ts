import { describe, expect, test } from 'claude-code/testing'

import { derive, duration, launchText } from '../hooks/model'
import { claim, issue, NOW, seed } from './fixtures'
import type { Issue, Raw } from '../types'

const withIssues = (issues: Issue[], description = '# Demo') =>
  seed({ issues, projects: [{ id: 'p1', title: 'feat/demo', description, repository_refs: [] }] })

describe('states', () => {
  test('follow the precedence offline, degraded, nosession, pending, noproject, ready', async () => {
    expect(derive(seed({ reachable: false, degraded: true, assoc: null }), NOW).state).toBe('offline')
    expect(derive(seed({ degraded: true, assoc: null }), NOW).state).toBe('degraded')
    expect(derive(seed({ assoc: null, projectId: null }), NOW).state).toBe('nosession')
    expect(derive(seed({ assoc: { ...seed().assoc!, status: 'pending' }, projectId: null }), NOW).state).toBe('pending')
    expect(derive(seed({ projectId: null }), NOW).state).toBe('noproject')
    expect(derive(seed({ projectId: 'gone' }), NOW).state).toBe('noproject')
    expect(derive(seed(), NOW).state).toBe('ready')
  })
})

describe('derived view', () => {
  test('ready, blocked, claimed by others and waiting', async () => {
    const v = derive(seed(), NOW)
    expect(v.work.map(i => i.id)).toEqual(['t1', 't2', 't3', 't4'])
    expect(v.ready.map(i => i.title)).toEqual(['Add cart model'])
    expect(v.blocked.map(b => `${b.issue.title} <- ${b.by.join(' + ')}`))
      .toEqual(['Add payment API client <- Which payment provider?', 'Checkout endpoint <- Add cart model + Add payment API client'])
    expect(v.othersClaims.map(c => `${c.title} ${c.owner} ${duration(c.remainingMs)}`)).toEqual(['Receipt email other 50m'])
    expect(v.waiting.map(w => [w.kind, w.command])).toEqual([
      ['decision', '/planner feat/demo decision: Which payment provider?'],
      ['needs-info', '/triage feat/demo:Refund flow unclear'],
      ['ready-for-human', 'Show me the lit issue "Review copy" (q2) in feat/demo and what it needs from me.'],
    ])
    expect(v.hasSpec).toBe(true)
    expect(v.settledDecisions).toBe(1)
  })

  test('closed blockers do not block; unknown blockers do', async () => {
    const v = derive(withIssues([
      issue('a', 'A', { labels: ['ready-for-agent'], state: 'closed' }),
      issue('b', 'B', { labels: ['ready-for-agent'], blocked_by: ['a'] }),
      issue('c', 'C', { labels: ['ready-for-agent'], blocked_by: ['0123456789abcdef'] }),
    ]), NOW)
    expect(v.ready.map(i => i.id)).toEqual(['b'])
    expect(v.blocked.map(b => b.by)).toEqual([['01234567']])
  })

  test('ideas, findings, decisions and waiting issues are never ready work', async () => {
    const v = derive(withIssues([
      issue('x', 'Idea', { labels: ['idea'] }),
      issue('y', 'Decision with the label', { labels: ['decision', 'ready-for-agent'] }),
      issue('z', 'Waiting with the label', { labels: ['needs-info', 'ready-for-agent'] }),
    ]), NOW)
    expect(v.work).toEqual([])
    expect(v.ready).toEqual([])
  })

  test('mine and own claims come from this session; a foreign claim keeps its title', async () => {
    const raw = seed({ claims: [claim('t1', 'me', 24), claim('elsewhere', 'me', 3)], otherTitles: { elsewhere: 'Other project task' } })
    raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', 24) } : i))
    const v = derive(raw, NOW)
    expect(v.mine.map(i => i.id)).toEqual(['t1'])
    expect(v.ready.map(i => i.id)).toEqual([])
    expect(v.ownClaims.map(c => [c.title, c.remainingMs])).toEqual([['Other project task', 180000], ['Add cart model', 1440000]])
    expect(v.othersClaims.map(c => c.issueId)).toEqual(['t4'])
  })

  test('the specification is a heading starting with "Shared specification"', async () => {
    const spec = (d: string) => derive(withIssues([], d), NOW).hasSpec
    expect(spec('# P\n\n## Shared specification: lit UI\n')).toBe(true)
    expect(spec('# Shared specification')).toBe(true)
    expect(spec('We have no shared specification yet.')).toBe(false)
    expect(spec('## Not a Shared specification')).toBe(false)
  })
})

describe('recommendation', () => {
  const rec = (raw: Raw) => derive(raw, NOW).recommend
  const ticket = (id: string, extra: Partial<Issue> = {}) => issue(id, `Ticket ${id}`, { labels: ['ready-for-agent'], ...extra })

  test('no issues: planner', async () => {
    expect(rec(withIssues([]))).toEqual({ flow: 'planner', reason: 'The project has no issues yet: start planning.', arg: null, command: '/planner feat/demo' })
  })

  test('a claim of this session: impl on that ticket, before open decisions', async () => {
    const raw = seed({ claims: [claim('t1', 'me', 24)] })
    raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', 24) } : i))
    expect(rec(raw)).toEqual({ flow: 'impl', reason: 'You hold the claim on this ticket.', arg: 'Add cart model', command: '/impl feat/demo:Add cart model' })
  })

  test('with several claims of this session: the one expiring first, as the band shows it', async () => {
    const raw = withIssues([ticket('a', { claim: claim('a', 'me', 40) }), ticket('b', { claim: claim('b', 'me', 10) })])
    raw.claims = [claim('a', 'me', 40), claim('b', 'me', 10)]
    expect(rec(raw).command).toBe('/impl feat/demo:Ticket b')
  })

  test('open decisions: planner on the first', async () => {
    const raw = withIssues([issue('d2', 'Later decision', { labels: ['decision'] }), ticket('a')])
    raw.issues.unshift(issue('d1', 'First decision', { labels: ['decision'], created_at: '2026-01-01T00:00:00Z' }))
    expect(rec(raw)).toEqual({ flow: 'planner', reason: '2 open decisions to settle.', arg: 'First decision', command: '/planner feat/demo decision: First decision' })
  })

  test('settled decisions and no specification: to-spec', async () => {
    expect(rec(withIssues([issue('d', 'D', { labels: ['decision'], state: 'closed' })])).command).toBe('/to-spec feat/demo')
  })

  test('a specification and no work tickets: to-tickets', async () => {
    const spec = '## Shared specification'
    expect(rec(withIssues([issue('d', 'D', { labels: ['decision'], state: 'closed' })], spec)))
      .toEqual({ flow: 'to-tickets', reason: 'The specification has no implementation tickets yet.', arg: null, command: '/to-tickets feat/demo' })
  })

  test('a specification and every ticket closed: nothing, the tickets are done', async () => {
    const spec = '## Shared specification'
    expect(rec(withIssues([ticket('a', { state: 'closed' })], spec)))
      .toEqual({ flow: null, reason: 'The implementation ticket is closed.', arg: null, command: null })
    expect(rec(withIssues([ticket('a', { state: 'closed' }), ticket('b', { state: 'closed' }), issue('i', 'Idea')], spec)).reason)
      .toBe('All 2 implementation tickets are closed.')
  })

  test('two or more ready: orchestrate the milestone holding most, else the whole project', async () => {
    const raw = withIssues([ticket('a'), ticket('b'), ticket('c')])
    expect(rec(raw)).toEqual({ flow: 'orchestrate', reason: '3 tickets are ready.', arg: null, command: '/orchestrate feat/demo' })
    raw.milestones = [
      { id: 'm1', title: 'M1', issue_ids: ['a'], closed: 0, total: 1 },
      { id: 'm2', title: 'M2 MVP', issue_ids: ['b', 'c'], closed: 0, total: 2 },
    ]
    expect(rec(raw).command).toBe('/orchestrate feat/demo milestone M2 MVP')
  })

  test('one ready: impl on it', async () => {
    expect(rec(withIssues([ticket('a'), ticket('b', { blocked_by: ['a'] })])).command).toBe('/impl feat/demo:Ticket a')
  })

  test('otherwise nothing, with the reason', async () => {
    const raw = withIssues([ticket('a', { blocked_by: ['x'] }), ticket('b', { blocked_by: ['y'] }), ticket('c', { claim: claim('c', 'other', 20) })])
    expect(rec(raw)).toEqual({ flow: null, reason: 'Nothing to start: 2 blocked, 1 claimed by others.', arg: null, command: null })
    expect(rec(withIssues([issue('i', 'Idea')])).reason).toBe('Nothing to start.')
  })
})

describe('commands', () => {
  test('exact prompt texts', async () => {
    expect(launchText('planner', 'feat/x', null)).toBe('/planner feat/x')
    expect(launchText('planner', 'feat/x', 'Which DB?')).toBe('/planner feat/x decision: Which DB?')
    expect(launchText('to-spec', 'feat/x', null)).toBe('/to-spec feat/x')
    expect(launchText('to-tickets', 'feat/x', null)).toBe('/to-tickets feat/x')
    expect(launchText('to-tickets', 'feat/x', 'M1')).toBe('/to-tickets feat/x milestone M1')
    expect(launchText('impl', 'feat/x', 'Add cart')).toBe('/impl feat/x:Add cart')
    expect(launchText('orchestrate', 'feat/x', null)).toBe('/orchestrate feat/x')
    expect(launchText('orchestrate', 'feat/x', 'M1')).toBe('/orchestrate feat/x milestone M1')
  })

  test('durations', async () => {
    expect([duration(3 * 60000 + 5000), duration(90 * 60000), duration(42000), duration(0)]).toEqual(['3m', '1h30m', '42s', 'expired'])
  })
})
