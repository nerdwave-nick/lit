import { describe, expect, test } from 'claude-code/testing'

import { claimNotices } from '../hooks/notices'
import type { Watched } from '../hooks/notices'
import type { ClaimView } from '../types'

const MIN = 60000
const T0 = Date.parse('2026-11-20T00:00:00Z')
const own = (issueId: string, title: string, expiresAt: number): ClaimView =>
  ({ issueId, title, expiresAt, remainingMs: 0, owner: 'me' })

// Feeds successive refreshes (time, own claims) through claimNotices and returns every toast.
function run(steps: [number, ClaimView[] | null][]): string[][] {
  let watched: Watched = {}
  return steps.map(([now, claims]) => {
    const r = claimNotices(watched, claims, now)
    watched = r.watched
    return r.toasts
  })
}

describe('claim toasts', () => {
  test('one toast when a claim falls under 5 minutes and one when it has expired', async () => {
    const c = own('t1', 'Add cart model', T0 + 10 * MIN)
    expect(run([
      [T0, [c]],
      [T0 + 4 * MIN, [c]],
      [T0 + 6 * MIN, [c]],
      [T0 + 7 * MIN, [c]],
      [T0 + 10 * MIN + 1000, []],
      [T0 + 11 * MIN, []],
    ])).toEqual([[], [], ['lit: claim on "Add cart model" expires in 4m'], [], ['lit: claim on "Add cart model" has expired'], []])
  })

  test('a claim still listed after its expiry toasts once', async () => {
    const c = own('t1', 'A', T0 + MIN)
    expect(run([[T0 + 2 * MIN, [c]], [T0 + 3 * MIN, [c]]])).toEqual([['lit: claim on "A" has expired'], []])
  })

  test('a renewed claim can warn again; a released one never expires', async () => {
    const c = own('t1', 'A', T0 + 4 * MIN)
    const renewed = own('t1', 'A', T0 + 34 * MIN)
    expect(run([
      [T0, [c]],
      [T0 + MIN, [renewed]],
      [T0 + 30 * MIN, [renewed]],
      [T0 + 31 * MIN, []],
      [T0 + 40 * MIN, []],
    ])).toEqual([['lit: claim on "A" expires in 4m'], [], ['lit: claim on "A" expires in 4m'], [], []])
  })

  test('claims that could not be read change nothing', async () => {
    const c = own('t1', 'A', T0 + 10 * MIN)
    expect(run([[T0, [c]], [T0 + 11 * MIN, null], [T0 + 12 * MIN, []]])).toEqual([[], [], ['lit: claim on "A" has expired']])
  })
})
