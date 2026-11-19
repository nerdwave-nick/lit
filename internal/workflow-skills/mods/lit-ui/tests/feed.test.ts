import { describe, expect, test } from 'claude-code/testing'

import { buildFeed, feedPath } from '../hooks/feed'
import { derive } from '../hooks/model'
import { claim, NOW, seed } from './fixtures'

describe('status feed', () => {
  test('the snapshot a user status line reads', async () => {
    const raw = seed({ claims: [claim('t1', 'me', 24)], stage: 'impl', errors: ['milestones list: boom'] })
    raw.issues = raw.issues.map(i => (i.id === 't1' ? { ...i, claim: claim('t1', 'me', 24) } : i))
    expect(buildFeed(derive(raw, NOW), NOW)).toEqual({
      updatedAt: '2026-11-20T00:00:00.000Z',
      state: 'ready',
      project: 'feat/demo',
      claims: [{ title: 'Add cart model', remainingMs: 1440000 }],
      stage: 'impl',
      ready: 0,
      waiting: 3,
      recommend: { flow: 'impl', reason: 'You hold the claim on this ticket.' },
      errors: ['milestones list: boom'],
    })
  })

  test('outside ready it still says the state', async () => {
    expect(buildFeed(derive(seed({ reachable: false }), NOW), NOW)).toEqual({
      updatedAt: '2026-11-20T00:00:00.000Z', state: 'offline', project: null, claims: [], stage: null, ready: 0, waiting: 0,
      recommend: { flow: null, reason: '' }, errors: [],
    })
  })

  test('one file per Claude session under the workflow state directory', async () => {
    expect(feedPath('/home/u/.local/state/lit/local', 'abc-123')).toBe('/home/u/.local/state/lit/local/ui-feed/abc-123.json')
    expect(feedPath('C:\\lit\\local', 'abc')).toBe('C:\\lit\\local\\ui-feed\\abc.json')
  })
})
