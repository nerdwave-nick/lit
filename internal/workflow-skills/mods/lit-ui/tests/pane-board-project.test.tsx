// The pane's Board and Project views as the engine runs them, against the fake lit and tracker
// of world.ts.
import { describe, expect, test } from 'claude-code/testing'

import { items, minutes, PANE, paneProps, paneText, ready, start, T, ticket, world } from './world'
import type { World } from './world'

const pane = async ($: any, cols = 100) => $.ui.mount({ ...PANE, props: paneProps(cols) })
const project = (id: string, title: string, refs: string[] = []) => ({ id, title, description: '# D', repository_refs: refs })
const projects = (w: World, ...xs: object[]) => (w.lit[`${T} projects list --all`] = { stdout: items(...xs) })
const ORIGIN = 'git@github.com:o/r.git'
// feat/demo selected; feat/zeta and fix/alpha belong to this repository; docs/other does not.
const picking = (): World => {
  const w = { ...ready(), origin: ORIGIN }
  projects(w, project('p1', 'feat/demo'), project('p2', 'feat/zeta', ['https://github.com/o/r']),
    project('p3', 'fix/alpha', ['https://GitHub.com/O/R']), project('p4', 'docs/other', ['https://github.com/x/y']))
  return w
}
const noProject = (w: World): World => {
  w.lit[`${T} session get`] = { stdout: items({ project_id: null, stage: null }) }
  return w
}
const ok = (stdout = items()) => ({ stdout })
const CREATE = `${T} projects create --project-title feat/new-thing --repository https://github.com/o/r`
const SELECT_NEW = `${T} session set --project title:feat/new-thing`

describe('Board', () => {
  test('summary, rows by state, the longest chain and the milestones; read-only', async ($, on) => {
    const w = ready()
    w.lit[`${T} issues list --all --project p1`] = { stdout: items(
      ticket('t1', { title: 'Add cart model' }),
      ticket('t2', { title: 'Checkout endpoint', blocked_by: ['t1'] }),
      ticket('t3', { title: 'Receipt email', claim: { issue_id: 't3', owner_client_id: 'other', owner_actor: { kind: 'agent', name: 'seed-agent' }, expires_at: minutes(50) } }),
      ticket('t4', { title: 'Scaffold', state: 'closed', updated_at: minutes(-5) }),
    ) }
    w.lit[`${T} milestones list --all --project p1`] = { stdout: items({ id: 'm1', title: 'M1 MVP', issue_ids: ['t1'], progress: { closed: 1, total: 4 } }) }
    const { clock } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-board' })
    const { texts, buttons } = await paneText(ui)
    expect(texts).toContain('1 ready · 1 blocked · 1 claimed by others · 1 done')
    for (const row of ['●  |Add cart model', '◌  |Checkout endpoint|  ← Add cart model', '◐  |Receipt email|  seed-agent · 50m', '✓  |Scaffold'])
      expect(texts).toContain(row)
    expect(texts).toContain('LONGEST CHAIN|Add cart model → Checkout endpoint')
    expect(texts).toContain(`M1 MVP|  |${'█'.repeat(6)}|${'░'.repeat(18)}|  1 of 4 done`)
    expect(texts.split('|').at(-1)).toBe('h l b p views · Esc close')
    expect(buttons.every(b => b.startsWith('tab-'))).toBe(true)
  })

  test('no implementation tickets yet; narrow panes draw shorter bars and no meta', async ($, on) => {
    const w = ready()
    w.lit[`${T} issues list --all --project p1`] = { stdout: items(ticket('d1', { labels: ['decision'] })) }
    w.lit[`${T} milestones list --all --project p1`] = { stdout: items({ id: 'm1', title: 'M1', issue_ids: [], progress: { closed: 0, total: 2 } }) }
    const { clock } = world(on, w)
    await start($, clock)
    const ui = await pane($, 50)
    await ui.press({ key: 'tab-board' })
    const { texts } = await paneText(ui)
    expect(texts).toContain('No implementation tickets yet · 1 other open issue')
    expect(texts).not.toContain('LONGEST CHAIN')
    expect(texts).toContain(`M1|  |${'░'.repeat(12)}|h l b p`)
  })
})

describe('Project', () => {
  test('the current project and this repository\'s, then more… for the others; never selects by itself', async ($, on) => {
    const { clock, seen } = world(on, picking())
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    await clock.advance(11000)
    let { texts, buttons } = await paneText(ui)
    expect(texts).toContain('CHOOSE A PROJECT')
    expect(buttons.filter(b => !b.startsWith('tab-'))).toEqual(['project-p1', 'project-p2', 'project-p3', 'more', 'new'])
    expect(texts).toContain('● feat/demo|  current')
    expect(texts).toContain('feat/zeta|  this repository')
    expect(texts).toContain('1 other project')
    expect((await ui.findAll({ type: 'Button' })).filter((b: any) => b.key.startsWith('project-') || b.key === 'more')
      .map((b: any) => b.props.hotkey)).toEqual(['1', '2', '3', '4'])
    expect(seen.runs.filter(r => r.argv === 'git remote get-url origin').map(r => r.cwd)).toEqual(['/checkout'])
    expect(seen.runs.some(r => r.argv.includes('session set'))).toBe(false)
    await ui.press({ key: 'more' })
    ;({ buttons } = await paneText(ui))
    expect(buttons.filter(b => b.startsWith('project-'))).toEqual(['project-p1', 'project-p2', 'project-p3', 'project-p4'])
    expect(buttons).not.toContain('more')
    expect(texts.split('|').at(-1)).toBe('1–4 select · n new project · Esc close')
  })

  test('choosing a project selects it with session set and goes Home', async ($, on) => {
    const w = picking()
    w.lit[`${T} session set --project p2`] = ok()
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    await ui.press({ key: 'project-p2' })
    expect(seen.runs.filter(r => r.argv.includes('session set')).map(r => r.argv)).toEqual([`${T} session set --project p2`])
    expect(seen.toasts).toContain('lit: selected feat/zeta')
    expect((await paneText(ui)).texts).toContain('NEXT STEP')
  })

  test('with no project selected: Project, the other tabs dim, this repository\'s projects', async ($, on) => {
    const { clock } = world(on, noProject(picking()))
    await start($, clock)
    const ui = await pane($)
    const { texts, buttons } = await paneText(ui)
    expect(texts).toContain('lit · |no project|Home|Launch|Board|Project')
    expect(buttons.filter(b => b.startsWith('tab-'))).toEqual(['tab-project'])
    expect(buttons.filter(b => b.startsWith('project-'))).toEqual(['project-p2', 'project-p3'])
    expect(texts).not.toContain('current')
  })

  test('the new title is checked only after typing, in red, as the tracker would', async ($, on) => {
    const { clock, seen } = world(on, picking())
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    expect((await ui.find({ key: 'new' }))?.props.hotkey).toBe('n')
    expect((await ui.find({ key: 'new-title' }))?.props.placeholder).toBe('feat/my-effort')
    expect((await paneText(ui)).texts).toContain('Creates it, selects it and puts /planner in the prompt.')
    const red = async () => (await ui.findAll({ type: 'Text' })).filter((t: any) => t.props.color === 'error').map((t: any) => t.text)
    expect(await red()).toEqual([])
    for (const [typed, problem] of [
      ['nope/x', 'Start with a prefix: feat'],
      ['feat/a b', 'After the prefix: letters, digits, - and _, each part starting with a letter or digit'],
      ['FEAT/Zeta', 'Start with a prefix: feat'],
      ['feat/Zeta', 'A project with this title exists'],
      [`feat/${'x'.repeat(124)}`, 'At most 128 characters'],
    ]) {
      await ui.input({ key: 'new-title', text: typed!, kind: 'change' })
      const shown = await ui.find({ type: 'Text', text: problem! })
      expect([typed, shown?.props.color]).toEqual([typed, 'error'])
    }
    await ui.input({ key: 'new-title', text: 'feat/a b' })
    expect(seen.runs.some(r => r.argv.includes('projects create'))).toBe(false)
    await ui.input({ key: 'new-title', text: 'feat/ok', kind: 'change' })
    expect(await red()).toEqual([])
    await ui.input({ key: 'new-title', text: '', kind: 'change' })
    expect(await red()).toEqual(['Enter a title such as feat/my-effort'])
  })

  test('create: projects create with the https repository ref, session set, /planner in the prompt, pane closed', async ($, on) => {
    const w = picking()
    w.lit[CREATE] = ok(items(project('p9', 'feat/new-thing', ['https://github.com/o/r'])))
    w.lit[SELECT_NEW] = ok()
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    await ui.input({ key: 'new-title', text: ' feat/new-thing ' })
    expect(seen.runs.filter(r => /projects create|session set/.test(r.argv)).map(r => [r.argv, r.cwd])).toEqual([
      [CREATE, '/wf/lit-ui'], [SELECT_NEW, '/wf/lit-ui']])
    expect([seen.fills, seen.closes]).toEqual([['/planner feat/new-thing'], ['plugin']])
    expect(seen.toasts).toContain('lit: created and selected feat/new-thing')
  })

  test('outside a repository the project is created without one', async ($, on) => {
    const w = { ...picking(), origin: null }
    w.lit[`${T} projects create --project-title feat/new-thing`] = ok()
    w.lit[SELECT_NEW] = ok()
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    await ui.input({ key: 'new-title', text: 'feat/new-thing' })
    expect(seen.fills).toEqual(['/planner feat/new-thing'])
  })

  test('a refused create shows once and stays in red; nothing is selected or filled', async ($, on) => {
    const w = picking()
    w.lit[CREATE] = { exitCode: 1, stdout: JSON.stringify({ error: { code: 'conflict', message: 'project title exists' } }) }
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-project' })
    await ui.input({ key: 'new-title', text: 'feat/new-thing' })
    expect(seen.runs.some(r => r.argv.includes('session set'))).toBe(false)
    expect([seen.fills, seen.closes]).toEqual([[], []])
    expect(seen.toasts).toContain('lit: create project failed: project title exists')
    const row = await ui.find({ type: 'Text', text: /^⚠/ })
    expect([row?.text, row?.props.color]).toEqual(['⚠ create project failed: project title exists', 'error'])
  })
})
