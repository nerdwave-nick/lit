import { describe, expect, test } from 'claude-code/testing'

import { bar, board, longestChain } from '../hooks/board'
import { derive } from '../hooks/model'
import { claim, issue, NOW, seed } from './fixtures'
import type { Issue, Raw } from '../types'

const view = (raw: Raw = seed()) => derive(raw, NOW)
const rows = (raw?: Raw) => board(view(raw)).rows.map(r => `${r.glyph} ${r.title}${r.meta ? ` | ${r.meta}` : ''}`)
const done = (id: string, title: string, closedMinutesAgo: number): Issue =>
  issue(id, title, { labels: ['ready-for-agent'], state: 'closed', updated_at: new Date(NOW - closedMinutesAgo * 60000).toISOString() })

describe('Board', () => {
  test('summary of the non-zero counts; rows ready, blocked with their blockers, others, done', async () => {
    const b = board(view())
    expect(b.summary).toBe('1 ready · 2 blocked · 1 claimed by others · 1 done')
    expect(rows()).toEqual([
      '● Add cart model',
      '◌ Add payment API client | ← Which payment provider?',
      '◌ Checkout endpoint | ← Add cart model, Add payment API client',
      '◐ Receipt email | other · 50m',
      '✓ Scaffold',
    ])
    expect(b.rows.map(r => r.tone)).toEqual(['success', 'dim', 'dim', 'warning', 'dim'])
  })

  test('this session\'s tickets lead as in progress, with the time left on the claim', async () => {
    const raw = seed({ claims: [claim('t1', 'me', 24)] })
    raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', 24) } : i))
    expect(board(view(raw)).summary).toBe('1 in progress · 2 blocked · 1 claimed by others · 1 done')
    expect(rows(raw)[0]).toBe('◷ Add cart model | you · 24m')
    expect(board(view(raw)).rows[0]!.tone).toBe('accent')
  })

  test('each ticket is counted once: a blocked ticket another agent claimed is blocked', async () => {
    const raw = seed()
    raw.issues = raw.issues.map(i => (i.id === 't4' ? { ...i, blocked_by: ['t1'] } : i))
    expect(board(view(raw)).summary).toBe('1 ready · 3 blocked · 1 done')
    expect(rows(raw)).toContain('◌ Receipt email | ← Add cart model')
  })

  test('the last 3 done, most recently closed first; the count says all of them', async () => {
    const raw = seed({ issues: [done('a', 'A', 50), done('b', 'B', 10), done('c', 'C', 40), done('d', 'D', 5), done('e', 'E', 30)] })
    expect(board(view(raw)).summary).toBe('5 done')
    expect(rows(raw)).toEqual(['✓ D', '✓ B', '✓ E'])
  })

  test('no implementation tickets: the other open issues are counted', async () => {
    const raw = seed({ issues: [issue('d1', 'Q?', { labels: ['decision'] }), issue('i1', 'Idea', { labels: ['idea'] }), issue('x', 'Old', { state: 'closed' })] })
    expect(board(view(raw))).toEqual({ summary: 'No implementation tickets yet · 2 other open issues', rows: [], chain: [], milestones: [] })
    const one = seed({ issues: [issue('i1', 'Idea', { labels: ['idea'] })] })
    expect(board(view(one)).summary).toBe('No implementation tickets yet · 1 other open issue')
    expect(board(view(seed({ issues: [] }))).summary).toBe('No implementation tickets yet')
  })

  test('LONGEST CHAIN of open blockers, root first, only when longer than one', async () => {
    expect(board(view()).chain).toEqual(['Which payment provider?', 'Add payment API client', 'Checkout endpoint'])
    const flat = seed({ issues: [issue('t1', 'A', { labels: ['ready-for-agent'] }), issue('t2', 'B', { labels: ['ready-for-agent'] })] })
    expect(board(view(flat)).chain).toEqual([])
  })

  test('the chain skips closed and unknown blockers and survives a cycle', async () => {
    const issues = [
      issue('a', 'A', { state: 'closed' }),
      issue('b', 'B', { blocked_by: ['a', 'elsewhere'] }),
      issue('c', 'C', { blocked_by: ['b', 'd'] }),
      issue('d', 'D', { blocked_by: ['c'] }),
    ]
    const chain = longestChain(issues)
    expect(chain.length).toBe(3)
    expect(new Set(chain).size).toBe(3)
    expect(longestChain([issue('x', 'X', { blocked_by: ['x'] })])).toEqual(['X'])
  })

  test('milestone progress bars with their counts', async () => {
    const raw = seed({ milestones: [
      { id: 'm1', title: 'M1 MVP', issue_ids: [], closed: 2, total: 5 },
      { id: 'm2', title: 'Empty', issue_ids: [], closed: 0, total: 0 },
    ] })
    expect(board(view(raw), 10).milestones).toEqual([
      { id: 'm1', title: 'M1 MVP', done: '████', todo: '░░░░░░', meta: '2 of 5 done' },
      { id: 'm2', title: 'Empty', done: '', todo: '░░░░░░░░░░', meta: '0 of 0 done' },
    ])
    expect(bar(5, 5, 4)).toEqual(['████', ''])
    expect(bar(9, 5, 4)).toEqual(['████', ''])
  })
})
