// The mod as the engine runs it: its hooks, with lit, the tracker and the files answered by the
// test beneath it. Each test starts a session, lets the first refresh run and reads the band.
import { describe, expect, test } from 'claude-code/testing'

import { ASSOC, bandText, items, ready, SID, start, T, world } from './world'
import type { World } from './world'

describe('lit-ui in a session', () => {
  test('ready: reads with the association, draws the band and writes the feed', async ($, on) => {
    const { clock, seen } = world(on, ready())
    await start($, clock)
    const { texts, buttons } = await bandText($)
    expect(texts).toBe('next /impl')
    expect(buttons).toEqual(['impl', 'lit'])
    expect(seen.runs.filter(r => r.argv.startsWith('lit ')).every(r => r.cwd === '/wf/lit-ui')).toBe(true)
    expect(seen.runs.some(r => r.argv.includes('workflow-session') && r.argv.includes(' run '))).toBe(false)
    const feed = JSON.parse(seen.files[`/wf/ui-feed/${SID}.json`]!)
    expect([feed.state, feed.project, feed.ready, feed.recommend]).toEqual(['ready', 'feat/demo', 1, { flow: 'impl', reason: 'One ticket is ready.' }])
    expect(JSON.stringify(seen.files)).not.toContain('TOKEN')
  })

  test('mod mode from config.json: project, stage and 1 fills the recommended command', async ($, on) => {
    const w = { ...ready(), config: '{"schema_version":1,"status":"mod","lit":"lit"}' }
    const { clock, seen } = world(on, w)
    await start($, clock)
    const { ui, texts, buttons } = await bandText($)
    expect(texts).toBe('🔥 feat/demo · impl')
    expect(buttons).toEqual(['impl', 'lit'])
    const reads = seen.runs.filter(r => r.argv === `${T} session get`).length
    await ui.press({ key: 'band-1' })
    expect(seen.fills).toEqual(['/impl feat/demo:Ticket t1'])
    expect(seen.runs.filter(r => r.argv === `${T} session get`).length).toBe(reads + 1)
  })

  test('a claim under 5 minutes: one toast, 3:renew renews it for 30 minutes on the press only', async ($, on) => {
    const w = { ...ready(), claimMinutes: 4 }
    const { clock, seen } = world(on, w)
    await start($, clock)
    await clock.advance(15000)
    expect(seen.toasts).toEqual(['claim on "Ticket t1" expires in 4m'])
    expect(seen.runs.some(r => /claims (renew|acquire|release)/.test(r.argv))).toBe(false)
    const { ui, texts, buttons } = await bandText($)
    expect(texts).toBe('◷ Ticket t1 3m left · next /impl')
    expect(buttons).toEqual(['renew', 'lit'])
    await ui.press({ key: 'band-3' })
    expect(seen.runs.filter(r => r.argv.includes('claims renew')).map(r => r.argv)).toEqual([`${T} claims renew t1 --for 30m`])
    expect(seen.toasts.at(-1)).toBe('claim renewed for 30m')
    expect((await bandText($)).buttons).toEqual(['impl', 'lit'])
  })

  test('an expired claim toasts once', async ($, on) => {
    const w: World = { ...ready(), claimMinutes: 0.05 }
    const { clock, seen } = world(on, w)
    await start($, clock)
    w.claimMinutes = null
    await clock.advance(10000)
    expect(seen.toasts).toEqual(['claim on "Ticket t1" expires in 3s', 'claim on "Ticket t1" has expired'])
  })

  test('buttons hide while a turn runs; the facts stay', async ($, on) => {
    const { clock } = world(on, ready())
    await start($, clock)
    const { texts, buttons } = await bandText($, true)
    expect([texts, buttons]).toEqual(['next /impl', []])
  })

  test('the states before ready and their actions', async ($, on) => {
    const w = ready()
    const { clock, seen } = world(on, w)
    await start($, clock)
    const now = async () => {
      await $.command.run({ command: 'lit-refresh', args: '', origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 120 } })
      return bandText($)
    }
    w.lit[`${T} session get`] = { stdout: items({ project_id: null, stage: null }) }
    let b = await now()
    expect([b.texts, b.buttons]).toEqual(['○ lit · no project selected', ['choose project']])
    await b.ui.press({ key: 'band-1' })
    expect(seen.opens).toEqual([{ id: 'lit-ui', title: 'lit', focus: true, closeOnEscape: true }])
    w.show = { exitCode: 0, stdout: JSON.stringify({ ...ASSOC, status: 'pending' }) }
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['○ lit · connection pending', ['reconcile']])
    await b.ui.press({ key: 'band-1' })
    expect(seen.runs.some(r => r.argv === `lit workflow-session --host claude --runtime-id ${SID} reconcile`)).toBe(true)
    expect(seen.toasts.at(-1)).toMatch(/^reconcile failed: /)
    w.show = { exitCode: 3, stdout: '{"error":{"code":"association_not_found","message":"none"}}' }
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['○ lit · not connected in this conversation', ['connect']])
    await b.ui.press({ key: 'band-1' })
    expect(seen.runs.some(r => r.argv === `lit workflow-session --host claude --runtime-id ${SID} fresh`)).toBe(true)
    expect(seen.toasts.at(-1)).toMatch(/^connect failed: /)
    w.meta = 503
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['⚫ lit · tracker needs recovery (docs/recovery.md)', ['retry']])
    w.meta = 'down'
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['⚫ lit · tracker not answering · 127.0.0.1:7411', ['retry']])
    const feed = JSON.parse(seen.files[`/wf/ui-feed/${SID}.json`]!)
    expect([feed.state, feed.project]).toEqual(['offline', null])
  })

  test('the agent running lit in Bash refreshes; other commands do not', async ($, on) => {
    const { clock, seen } = world(on, ready())
    on('tool.call', async () => ({ result: { stdout: '', stderr: '', interrupted: false }, text: '' }) as any)
    await start($, clock)
    const reads = () => seen.runs.filter(r => r.argv === `${T} session get`).length
    const before = reads()
    await $.tool.call({ tool: 'Bash', command: 'git status' } as any)
    await clock.settle()
    expect(reads()).toBe(before)
    await $.tool.call({ tool: 'Bash', command: 'lit claims list' } as any)
    await clock.settle()
    expect(reads()).toBe(before + 1)
  })

  test('a planning stage that clears suggests the following stage once, when the box can show it', async ($, on) => {
    const w = ready()
    const { clock, seen } = world(on, w)
    const stage = (s: string | null) => { w.lit[`${T} session get`] = { stdout: items({ project_id: 'p1', stage: s }) } }
    stage('planner')
    await start($, clock)
    await clock.advance(10000)
    expect(seen.suggestions).toEqual([])
    // Cleared during a turn: the box cannot show it yet, so it waits for the turn's end.
    w.canSuggest = false
    stage(null)
    await clock.advance(5000)
    expect(seen.suggestions).toEqual([{ text: '/to-spec feat/demo', isShown: false }])
    w.canSuggest = true
    await $.turn.complete({ answer: '', durationMs: 1, isAborted: false, turnId: 't', reason: 'answer' })
    await clock.settle()
    expect(seen.suggestions.at(-1)).toEqual({ text: '/to-spec feat/demo', isShown: true })
    const offers = seen.suggestions.length
    // Neither later polls nor later turns suggest it again.
    await clock.advance(20000)
    await $.turn.complete({ answer: '', durationMs: 1, isAborted: false, turnId: 't2', reason: 'answer' })
    await clock.settle()
    expect(seen.suggestions.length).toBe(offers)
    // impl clearing leads nowhere; to-tickets clearing suggests orchestrate.
    stage('impl')
    await clock.advance(5000)
    stage(null)
    await clock.advance(5000)
    stage('to-tickets')
    await clock.advance(5000)
    stage(null)
    await clock.advance(15000)
    expect(seen.suggestions.slice(offers)).toEqual([{ text: '/orchestrate feat/demo', isShown: true }])
  })

  test('/lit-ui debug reports the session and the last refresh; no claim token anywhere', async ($, on) => {
    const { clock, seen } = world(on, { ...ready(), claimMinutes: 20 })
    await start($, clock)
    const r = await $.command.run({ command: 'lit-ui', args: 'debug', origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 120 } })
    expect(r.text).toBe([
      'endpoint: http://127.0.0.1:7472',
      'status mode: both (development default; lit: lit)',
      'lit session: agent-1',
      'state: ready',
      'stage: impl',
      'last refresh: 2026-11-20T00:00:00.000Z, 0 ms',
    ].join('\n'))
    expect(JSON.stringify(seen.files)).not.toContain('TOKEN')
  })
})
