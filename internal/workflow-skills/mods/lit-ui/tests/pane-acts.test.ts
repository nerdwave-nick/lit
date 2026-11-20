import { describe, expect, test } from 'claude-code/testing'

import { derive } from '../hooks/model'
import { EMPTY_UI } from '../hooks/pane'
import { paneActs } from '../hooks/pane-acts'
import type { PaneIo } from '../hooks/pane-acts'
import { NOW, seed } from './fixtures'
import type { PaneUi, Raw } from '../types'

// A fake engine: the pane's state and every call the acts make, in order.
function fake(start: Partial<PaneUi> = {}, raw: Partial<Raw> = {}) {
  const state = { ui: { ...EMPTY_UI, ...start } as PaneUi, calls: [] as string[], trackerOk: true }
  const io: PaneIo = {
    view: async () => derive(seed(raw), NOW),
    ui: async () => state.ui,
    patch: async fn => void (state.ui = fn(state.ui)),
    focus: key => void state.calls.push(`focus ${key}`),
    close: async () => void state.calls.push('close'),
    fill: async text => void state.calls.push(`fill ${text}`),
    runLater: (command, args) => void state.calls.push(`run /${command} ${args}`),
    renew: async id => void state.calls.push(`renew ${id}`),
    release: async id => void state.calls.push(`release ${id}`),
    press: does => void state.calls.push(`press ${does.kind}`),
    tracker: async (steps, ok) => {
      state.calls.push(...steps.map(s => `lit ${s.args.join(' ')}`), `ok ${ok}`)
      return state.trackerOk
    },
  }
  return { state, acts: paneActs(io) }
}
// The acts start their work without waiting; let it settle.
const settled = async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve()
}

describe('pane acts', () => {
  test('a tab: Home focuses the next step, Launch the recommended workflow; a step or selection is dropped', async () => {
    const { state, acts } = fake({ step: 'impl', claim: 't1', confirm: 't1' })
    acts.tab('launch')
    await settled()
    expect(state.ui).toEqual({ ...EMPTY_UI, tab: 'launch' })
    acts.tab('home')
    await settled()
    expect(state.calls).toEqual(['focus flow-planner', 'focus next'])
  })

  test('choosing a workflow or a target moves the focus to put in prompt', async () => {
    const { state, acts } = fake({ tab: 'launch' })
    acts.pickFlow('impl')
    await settled()
    expect([state.ui.step, state.ui.sel]).toEqual(['impl', 0])
    acts.choose(2)
    await settled()
    expect(state.ui.sel).toBe(2)
    acts.back()
    await settled()
    expect(state.ui.step).toBe(null)
    expect(state.calls).toEqual(['focus put', 'focus put'])
  })

  test('Esc goes back from a target step, then lets the pane close', async () => {
    const { state, acts } = fake({ tab: 'launch', step: 'planner', sel: 1 })
    expect(await acts.escape()).toBe(true)
    expect(state.ui.step).toBe(null)
    expect(await acts.escape()).toBe(false)
    expect(state.calls).toEqual([])
  })

  test('fill and run now both leave the pane; run now goes through a timer; a plain prompt only fills', async () => {
    const { state, acts } = fake({ step: 'impl' })
    acts.fill('/impl feat/demo:Add cart model')
    await settled()
    expect(state.ui.step).toBe(null)
    acts.run('/to-spec feat/demo')
    await settled()
    acts.run('Show me the lit issue "Copy"')
    await settled()
    expect(state.calls).toEqual(['close', 'fill /impl feat/demo:Add cart model', 'close', 'run /to-spec feat/demo', 'close', 'fill Show me the lit issue "Copy"'])
  })

  test('a claim: selecting focuses renew; release only on the second press', async () => {
    const { state, acts } = fake()
    acts.selectClaim('t1')
    await settled()
    expect(state.ui.claim).toBe('t1')
    acts.release('t1')
    await settled()
    expect([state.ui.confirm, state.calls]).toEqual(['t1', ['focus renew-t1']])
    acts.release('t1')
    await settled()
    expect([state.ui.claim, state.ui.confirm, state.calls.at(-1)]).toEqual([null, null, 'release t1'])
    acts.selectClaim('t1')
    acts.renew('t1')
    await settled()
    expect(state.calls.at(-1)).toBe('renew t1')
    acts.selectClaim('t1')
    await settled()
    acts.selectClaim('t1')
    await settled()
    expect(state.ui.claim).toBe(null)
  })

  test('Project: n focuses the title; more… lists all, Esc folds it back, then lets the pane close', async () => {
    const { state, acts } = fake({ tab: 'project' })
    acts.focusTitle()
    acts.more()
    await settled()
    expect([state.calls, state.ui.allProjects]).toEqual([['focus new-title'], true])
    expect(await acts.escape()).toBe(true)
    expect(state.ui.allProjects).toBe(false)
    expect(await acts.escape()).toBe(false)
  })

  test('choosing a project selects it by ID and goes Home with the focus on the next step; a failure stays', async () => {
    const { state, acts } = fake({ tab: 'project', allProjects: true })
    acts.selectProject('p1')
    await settled()
    expect(state.calls).toEqual(['lit session set --project p1', 'ok selected feat/demo', 'focus next'])
    expect([state.ui.tab, state.ui.allProjects]).toEqual(['home', false])
    const failing = fake({ tab: 'project' })
    failing.state.trackerOk = false
    failing.acts.selectProject('p1')
    failing.acts.selectProject('unknown')
    await settled()
    expect([failing.state.calls, failing.state.ui.tab]).toEqual([['lit session set --project p1', 'ok selected feat/demo'], 'project'])
  })

  test('create: a refused title only shows; a good one is created with the repository, selected, planned', async () => {
    const { state, acts } = fake({ tab: 'project' }, { origin: 'git@github.com:o/r.git' })
    acts.create('feat/DEMO')
    await settled()
    expect([state.calls, state.ui.draft, state.ui.typed]).toEqual([[], 'feat/DEMO', true])
    acts.create(' feat/cart ')
    await settled()
    expect(state.calls).toEqual([
      'lit projects create --project-title feat/cart --repository https://github.com/o/r',
      'lit session set --project title:feat/cart',
      'ok created and selected feat/cart',
      'close',
      'fill /planner feat/cart',
    ])
    expect([state.ui.tab, state.ui.draft, state.ui.typed]).toEqual(['home', '', false])
  })

  test('a failed create leaves the pane open with the title as typed', async () => {
    const { state, acts } = fake({ tab: 'project', draft: 'feat/cart', typed: true })
    state.trackerOk = false
    acts.create('feat/cart')
    await settled()
    expect(state.calls).toEqual(['lit projects create --project-title feat/cart', 'lit session set --project title:feat/cart', 'ok created and selected feat/cart'])
    expect([state.ui.tab, state.ui.draft]).toEqual(['project', 'feat/cart'])
  })
})
