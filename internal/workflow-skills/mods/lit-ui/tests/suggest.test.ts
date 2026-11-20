import { describe, expect, test } from 'claude-code/testing'

import { derive } from '../hooks/model'
import { stageReading, suggestionShown, watchStage } from '../hooks/suggest'
import type { StageReading, StageWatch } from '../hooks/suggest'
import { NOW, seed } from './fixtures'

const at = (stage: string | null, extra: Partial<StageReading> = {}): StageReading =>
  ({ key: 'agent-1/me', projectId: 'p1', project: 'feat/demo', stage, ...extra })

// Feeds successive refreshes through watchStage; every suggestion offered is shown at once.
// Returns what each refresh suggested.
function run(readings: (StageReading | null)[]): (string | null)[] {
  let w: StageWatch = { seen: null, pending: null }
  return readings.map(r => {
    w = watchStage(w, r)
    const offered = w.pending
    if (offered) w = suggestionShown(w, offered)
    return offered
  })
}

describe('next-stage suggestion', () => {
  test('each planning stage that clears suggests the following stage once', async () => {
    expect(run([at('planner'), at(null), at(null), at(null)])).toEqual([null, '/to-spec feat/demo', null, null])
    expect(run([at('to-spec'), at(null), at(null)])).toEqual([null, '/to-tickets feat/demo', null])
    expect(run([at('to-tickets'), at(null), at(null)])).toEqual([null, '/orchestrate feat/demo', null])
  })

  test('each cleared stage suggests again; turns and polls in between do not', async () => {
    expect(run([at(null), at('planner'), at('planner'), at(null), at(null), at('to-spec'), at(null), at(null)]))
      .toEqual([null, null, null, '/to-spec feat/demo', null, null, '/to-tickets feat/demo', null])
  })

  test('no suggestion for other changes', async () => {
    expect(run([
      at(null), // the first reading only sets the baseline
      at('planner'), at('to-spec'), // changed, not cleared
      at('impl'), at(null), // impl, orchestrate and other stages lead nowhere
      at('orchestrate'), at(null),
      at('clarify'), at(null),
    ])).toEqual([null, null, null, null, null, null, null, null, null])
    // A session already without a stage when first seen.
    expect(run([at(null), at(null)])).toEqual([null, null])
  })

  test('no suggestion without a project, after a project change or for another lit session', async () => {
    expect(run([at('planner'), at(null, { projectId: null, project: null })])).toEqual([null, null])
    expect(run([at('planner'), at(null, { projectId: 'p2', project: 'feat/other' })])).toEqual([null, null])
    expect(run([at('planner'), at(null, { key: 'agent-2/me2' })])).toEqual([null, null])
  })

  test('a refresh that could not read the stage is no observation', async () => {
    expect(run([at('planner'), null, null, at(null), at(null)])).toEqual([null, null, null, '/to-spec feat/demo', null])
    expect(run([at(null), null, at(null)])).toEqual([null, null, null])
  })

  test('a suggestion not yet shown waits, until shown or superseded', async () => {
    let w = watchStage(watchStage({ seen: null, pending: null }, at('planner')), at(null))
    expect(w.pending).toBe('/to-spec feat/demo')
    // Not shown (a turn ran, the box held text): offered again at the next chance.
    w = watchStage(w, at(null))
    expect(w.pending).toBe('/to-spec feat/demo')
    // Shown once, then never again.
    w = suggestionShown(w, '/to-spec feat/demo')
    expect(watchStage(w, at(null)).pending).toBe(null)
    // A new stage, a lost project or another project drops a waiting suggestion.
    const waiting = watchStage(watchStage({ seen: null, pending: null }, at('to-spec')), at(null))
    expect(watchStage(waiting, at('to-tickets')).pending).toBe(null)
    expect(watchStage(waiting, at(null, { projectId: null, project: null })).pending).toBe(null)
    expect(watchStage(waiting, at(null, { projectId: 'p2', project: 'feat/other' })).pending).toBe(null)
    // A suggestion shown after it was superseded changes nothing.
    const next = watchStage(waiting, at('to-tickets'))
    expect(suggestionShown(next, '/to-tickets feat/demo')).toEqual(next)
  })

  test('the reading: ready and noproject sessions with a stage read; anything else unknown', async () => {
    const reading = (overrides: Parameters<typeof seed>[0]) => {
      const raw = seed(overrides)
      return stageReading(raw, derive(raw, NOW))
    }
    expect(reading({ stage: 'planner' })).toEqual({ key: 'agent-1/me', projectId: 'p1', project: 'feat/demo', stage: 'planner' })
    expect(reading({ projectId: null, stage: null })).toEqual({ key: 'agent-1/me', projectId: null, project: null, stage: null })
    expect(reading({ stage: null, stageKnown: false })).toBe(null)
    expect(reading({ reachable: false })).toBe(null)
    expect(reading({ degraded: true })).toBe(null)
    expect(reading({ assoc: null })).toBe(null)
    expect(reading({ assoc: { local_session: 'agent-1', endpoint: 'e', client_id: null, project_id: null, status: 'pending' } })).toBe(null)
  })
})
