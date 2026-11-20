import { describe, expect, test } from 'claude-code/testing'

import { derive } from '../hooks/model'
import { EMPTY_UI, flowRows, home, launchHints, slash, stepCommand, tabOf, targetStep } from '../hooks/pane'
import { claim, issue, NOW, seed } from './fixtures'
import type { PaneUi, Raw } from '../types'

const view = (raw: Raw = seed()) => derive(raw, NOW)
const ui = (patch: Partial<PaneUi> = {}): PaneUi => ({ ...EMPTY_UI, ...patch })
// This session holds the claim on "Add cart model" for `minutes`.
const holding = (minutes: number) => {
  const raw = seed({ claims: [claim('t1', 'me', minutes)] })
  raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', minutes) } : i))
  return view(raw)
}
const milestones = [{ id: 'm1', title: 'M1 MVP', issue_ids: ['t1', 't2'], closed: 1, total: 4 }]
const rowText = (r: { glyph: string; label: string; meta: string; enabled: boolean }) =>
  `${r.enabled ? '' : '(dim) '}${r.glyph} ${r.label}${r.meta ? ` | ${r.meta}` : ''}`

describe('Launch: the workflow list', () => {
  test('five workflows in fixed order; the recommended one marked; unavailable ones dim with the reason', async () => {
    expect(flowRows(view()).map(r => [r.flow, r.describe, r.meta, r.enabled, r.recommended])).toEqual([
      ['planner', 'settle open decisions', '★ recommended', true, true],
      ['to-spec', 'write the shared specification', '', true, false],
      ['to-tickets', 'cut implementation tickets', '', true, false],
      ['impl', 'implement one ready ticket', '1 ready', true, false],
      ['orchestrate', 'run ready tickets in parallel', 'needs 2+ ready tickets', false, false],
    ])
  })

  test('no specification and no ready tickets', async () => {
    const raw = seed({ issues: [issue('d1', 'Which payment provider?', { labels: ['decision'], state: 'closed' }), issue('t2', 'Blocked', { labels: ['ready-for-agent'], blocked_by: ['x'] })] })
    raw.projects = [{ ...raw.projects[0]!, description: '# Demo' }]
    const rows = flowRows(view(raw))
    expect(rows.map(r => [r.flow, r.meta, r.enabled])).toEqual([
      ['planner', '', true],
      ['to-spec', '★ recommended', true],
      ['to-tickets', 'needs a specification', false],
      ['impl', 'no ready tickets', false],
      ['orchestrate', 'needs 2+ ready tickets', false],
    ])
  })

  test('two ready tickets make orchestrate available; a held claim keeps impl available', async () => {
    const raw = seed()
    raw.issues = [...raw.issues.filter(i => i.labels.includes('ready-for-agent')), issue('t5', 'Search box', { labels: ['ready-for-agent'] })]
    expect(flowRows(view(raw)).find(r => r.flow === 'orchestrate')).toMatchObject({ meta: '★ recommended', enabled: true })
    const mine = holding(20)
    expect(flowRows(mine).find(r => r.flow === 'impl')).toMatchObject({ meta: '★ recommended', enabled: true })
  })
})

describe('Launch: target steps', () => {
  test('impl: mine and ready choosable; blocked and others claims dim and not choosable', async () => {
    const step = targetStep(holding(24), 'impl')
    expect(step.question).toBe('IMPLEMENT WHICH TICKET?')
    expect(step.items.map(rowText)).toEqual([
      '● Add cart model | yours · 24m',
      '(dim) ◌ Add payment API client | blocked by Which payment provider?',
      '(dim) ◌ Checkout endpoint | blocked by Add cart model, Add payment API client',
      '(dim) ◐ Receipt email | other · 50m',
    ])
    expect(targetStep(view(), 'impl').items.map(rowText)[0]).toBe('● Add cart model')
  })

  test('planner: open decisions, then the whole planning map', async () => {
    const step = targetStep(view(), 'planner')
    expect(step.question).toBe('SETTLE WHICH DECISION?')
    expect(step.items.map(rowText)).toEqual(['⚑ Which payment provider?', '· the whole planning map'])
    expect(stepCommand(view(), 'planner', 0)).toBe('/planner feat/demo decision: Which payment provider?')
    expect(stepCommand(view(), 'planner', 1)).toBe('/planner feat/demo')
  })

  test('to-tickets and orchestrate: milestones, then the whole project', async () => {
    const v = view(seed({ milestones }))
    for (const [flow, question] of [['to-tickets', 'TICKETS FOR WHICH SCOPE?'], ['orchestrate', 'ORCHESTRATE WHICH SCOPE?']] as const) {
      const step = targetStep(v, flow)
      expect(step.question).toBe(question)
      expect(step.items.map(rowText)).toEqual(['◆ M1 MVP | 1 of 4 done', '· the whole project'])
    }
    expect(stepCommand(v, 'to-tickets', 0)).toBe('/to-tickets feat/demo milestone M1 MVP')
    expect(stepCommand(v, 'to-tickets', 1)).toBe('/to-tickets feat/demo')
    expect(stepCommand(v, 'orchestrate', 0)).toBe('/orchestrate feat/demo milestone M1 MVP')
    expect(stepCommand(v, 'orchestrate', 1)).toBe('/orchestrate feat/demo')
  })

  test('to-spec has no target; its command stands alone', async () => {
    expect(targetStep(view(), 'to-spec').items).toEqual([])
    expect(stepCommand(view(), 'to-spec', 0)).toBe('/to-spec feat/demo')
  })

  test('choosable rows come first; a row that is not choosable gives no command', async () => {
    expect(stepCommand(view(), 'impl', 0)).toBe('/impl feat/demo:Add cart model')
    expect(stepCommand(view(), 'impl', 1)).toBe(null)
    expect(stepCommand(view(), 'impl', 9)).toBe(null)
    const raw = seed()
    raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, blocked_by: ['d1'] } : i))
    raw.issues.push(issue('t9', 'Late ticket', { labels: ['ready-for-agent'] }))
    expect(targetStep(view(raw), 'impl').items.map(rowText)[0]).toBe('● Late ticket')
  })

  test('hints: run now everywhere but orchestrate', async () => {
    expect(launchHints(view(), ui())).toBe('1–5 choose · Esc close')
    expect(launchHints(view(), ui({ step: 'impl' }))).toBe('Enter put in prompt · r run now · 1–4 choose · Esc back')
    expect(launchHints(view(seed({ milestones })), ui({ step: 'orchestrate' }))).toBe('Enter put in prompt · 1–2 choose · Esc back')
    expect(launchHints(view(), ui({ step: 'to-spec' }))).toBe('Enter put in prompt · r run now · Esc back')
    expect(launchHints(view(), ui({ step: 'impl', sel: 1 }))).toBe('1–4 choose · Esc back')
  })
})

describe('Home', () => {
  test('next step, my claims and what waits, numbered in that order', async () => {
    const h = home(holding(3), ui())
    expect(h.next).toEqual({ command: '/impl feat/demo:Add cart model', reason: 'You hold the claim on this ticket.', runNow: true })
    expect(h.claims.map(c => [c.n, c.claim.title, c.warn, c.selected])).toEqual([[1, 'Add cart model', true, false]])
    expect(h.waiting.map(w => [w.n, w.item.issue.title, w.item.kind])).toEqual([
      [2, 'Which payment provider?', 'decision'],
      [3, 'Refund flow unclear', 'needs-info'],
      [4, 'Review copy', 'ready-for-human'],
    ])
    expect(h.hints).toBe('Enter put in prompt · r run now · 1–4 choose · Esc close')
  })

  test('a selected claim offers renew and release; release asks for a second x', async () => {
    let h = home(holding(20), ui({ claim: 't1' }))
    expect(h.claims[0]!.selected).toBe(true)
    expect(h.next.runNow).toBe(false)
    expect(h.hints).toBe('r renew · x release · Esc close')
    h = home(holding(20), ui({ claim: 't1', confirm: 't1' }))
    expect(h.hints).toBe('x again to release · r renew · Esc close')
    expect(home(holding(20), ui({ claim: 'gone' })).claims[0]!.selected).toBe(false)
  })

  test('orchestrate never runs now; no recommendation leaves only the reason', async () => {
    const raw = seed()
    raw.issues = [...raw.issues.filter(i => i.labels.includes('ready-for-agent')), issue('t5', 'Search box', { labels: ['ready-for-agent'] })]
    const h = home(view(raw), ui())
    expect(h.next).toEqual({ command: '/orchestrate feat/demo', reason: '2 tickets are ready.', runNow: false })
    expect(h.hints).toBe('Enter put in prompt · Esc close')
    const stuck = seed({ issues: [issue('t2', 'Blocked', { labels: ['ready-for-agent'], blocked_by: ['x'] })] })
    expect(home(view(stuck), ui())).toEqual({ next: { command: null, reason: 'Nothing to start: 1 blocked.', runNow: false }, claims: [], waiting: [], hints: 'Esc close' })
  })
})

describe('shell', () => {
  test('without a project the pane shows Project', async () => {
    expect(tabOf(view(), ui({ tab: 'launch' }))).toBe('launch')
    expect(tabOf(view(seed({ projectId: null })), ui({ tab: 'home' }))).toBe('project')
  })

  test('run now splits a slash command; plain prompts do not run', async () => {
    expect(slash('/impl feat/demo:Add cart model')).toEqual({ command: 'impl', args: 'feat/demo:Add cart model' })
    expect(slash('/to-spec feat/demo')).toEqual({ command: 'to-spec', args: 'feat/demo' })
    expect(slash('Show me the lit issue')).toBe(null)
  })
})
