import { describe, expect, test } from 'claude-code/testing'

import { band } from '../hooks/band'
import type { BandModel } from '../hooks/band'
import { derive } from '../hooks/model'
import { claim, issue, NOW, seed } from './fixtures'
import type { Raw } from '../types'

const text = (m: BandModel) => [m.glyph, m.lead, ...m.facts.map(f => f.text)].filter(Boolean).join(' · ')
const keys = (m: BandModel) => m.buttons.map(b => `${b.key}:${b.label}`)
const view = (raw: Raw) => derive(raw, NOW)
// This session holds the claim on "Add cart model" for `minutes`.
const holding = (minutes: number, stage: string | null = 'impl') => {
  const raw = seed({ claims: [claim('t1', 'me', minutes)], stage })
  raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', minutes) } : i))
  return view(raw)
}

describe('band in ready', () => {
  test('mod: project, claim, waiting and stage; 1 launches the recommendation', async () => {
    const m = band(holding(24), 'mod')
    expect(m.glyph).toBe('🔥')
    expect(m.lead).toBe('feat/demo')
    expect(m.leadBold).toBe(true)
    expect(m.facts).toEqual([
      { text: '◷ Add cart model 24m', tone: 'text' },
      { text: '⚑ 3 waiting', tone: 'warning' },
      { text: 'impl', tone: 'dim' },
    ])
    expect(keys(m)).toEqual(['1:impl', '2:lit'])
    expect(m.buttons[0]!.does).toEqual({ kind: 'fill', command: '/impl feat/demo:Add cart model' })
  })

  test('both: only waiting and the next flow; project and claim are the status line\'s', async () => {
    const m = band(holding(24), 'both')
    expect([m.glyph, m.lead]).toEqual([null, ''])
    expect(text(m)).toBe('⚑ 3 waiting · next /impl')
    expect(keys(m)).toEqual(['1:impl', '2:lit'])
  })

  test('a claim under 5 minutes turns yellow with "left" in both modes and 3:renew replaces 1', async () => {
    for (const mode of ['mod', 'both'] as const) {
      const m = band(holding(3), mode)
      expect(m.facts[0]).toEqual({ text: '◷ Add cart model 3m left', tone: 'warning' })
      expect(keys(m)).toEqual(['3:renew', '2:lit'])
      expect(m.buttons[0]!.does).toEqual({ kind: 'renew', issueId: 't1' })
    }
  })

  test('zero counts and an empty stage are omitted; no 1 when nothing is recommended', async () => {
    const quiet = view(seed({ issues: [issue('a', 'A', { labels: ['ready-for-agent'], blocked_by: ['x'] })] }))
    expect(text(band(quiet, 'mod'))).toBe('🔥 · feat/demo')
    expect(keys(band(quiet, 'mod'))).toEqual(['2:lit'])
    expect(band(quiet, 'both').facts).toEqual([])
    expect(keys(band(quiet, 'both'))).toEqual(['2:lit'])
  })
})

describe('band in the other states', () => {
  test('each has its line and one action', async () => {
    const cases: [Partial<Raw>, string, string, string][] = [
      [{ projectId: null }, '○', 'lit · no project selected', '1:choose project'],
      [{ assoc: null }, '○', 'lit · not connected in this conversation', '1:connect'],
      [{ assoc: { ...seed().assoc!, status: 'pending' } }, '○', 'lit · connection pending', '1:reconcile'],
      [{ reachable: false, endpoint: 'http://127.0.0.1:7411' }, '⚫', 'lit · tracker not answering · 127.0.0.1:7411', '1:retry'],
      [{ degraded: true }, '⚫', 'lit · tracker needs recovery (docs/recovery.md)', '1:retry'],
    ]
    for (const [raw, glyph, line, key] of cases) {
      for (const mode of ['mod', 'both'] as const) {
        const m = band(view(seed(raw)), mode)
        expect([m.glyph, [m.lead, ...m.facts.map(f => f.text)].join(' · ')]).toEqual([glyph, line])
        expect(keys(m)).toEqual([key])
      }
    }
  })
})
