// The lit pane as the engine runs it: opened from the band and /lit-ui, its tabs, Home and
// Launch, against the fake lit and tracker of world.ts.
import { describe, expect, test } from 'claude-code/testing'

import { bandText, items, myClaim, PANE, paneProps, paneText, ready, start, T, ticket, world } from './world'
import type { World } from './world'

const COMPOSER = { origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 90 } } as const
const OPENED = { id: 'lit-ui', title: 'lit', focus: true, closeOnEscape: true }
const issues = (w: World, ...xs: object[]) => (w.lit[`${T} issues list --all --project p1`] = { stdout: items(...xs) })
// t1 claimed by this session for `m` minutes.
const holding = (m: number): World => {
  const w = { ...ready(), claimMinutes: m }
  issues(w, ticket('t1', { claim: myClaim(m) }), ticket('t2', { blocked_by: ['t1'] }))
  return w
}
const pane = async ($: any, cols = 100) => $.ui.mount({ ...PANE, props: paneProps(cols) })

describe('opening the pane', () => {
  test('2 on the band and /lit-ui open it focused, Esc closing it', async ($, on) => {
    const { clock, seen } = world(on, ready())
    await start($, clock)
    const { ui } = await bandText($)
    await ui.press({ key: 'band-2' })
    expect(seen.opens).toEqual([OPENED])
    const r = await $.command.run({ command: 'lit-ui', args: '', ...COMPOSER })
    expect(r.text).toBe('lit pane opened')
    expect(seen.opens).toEqual([OPENED, OPENED])
  })

  test('1:choose project opens it on Project; the other tabs are dim', async ($, on) => {
    const w = ready()
    w.lit[`${T} session get`] = { stdout: items({ project_id: null, stage: null }) }
    const { clock, seen } = world(on, w)
    await start($, clock)
    await (await bandText($)).ui.press({ key: 'band-1' })
    expect(seen.opens).toEqual([OPENED])
    const { texts, buttons } = await paneText(await pane($))
    expect(buttons).toEqual(['tab-project'])
    expect(texts).toContain('lit · |no project|Home|Launch|Board|Project')
  })

  test('header, tabs with h l b p, the current one bold and underlined, hints last', async ($, on) => {
    const { clock } = world(on, ready())
    await start($, clock)
    const ui = await pane($)
    expect((await paneText(ui)).buttons.slice(0, 4)).toEqual(['tab-home', 'tab-launch', 'tab-board', 'tab-project'])
    expect((await ui.findAll({ type: 'Button' })).slice(0, 4).map((b: any) => b.props.hotkey)).toEqual(['h', 'l', 'b', 'p'])
    const home = await ui.find({ type: 'Text', text: 'Home' })
    expect([home?.props.bold, home?.props.underline, (await ui.find({ type: 'Text', text: 'Launch' }))?.props.dimColor]).toEqual([true, true, true])
    expect((await paneText(ui)).texts).toMatch(/^lit · \|feat\/demo\|/)
    await ui.press({ key: 'tab-launch' })
    expect((await ui.find({ type: 'Text', text: 'Launch' }))?.props.bold).toBe(true)
    let t = (await paneText(ui)).texts
    expect(t).toContain('START A WORKFLOW')
    expect(t.split('|').at(-1)).toBe('1–5 choose · Esc close')
    await ui.press({ key: 'tab-board' })
    t = (await paneText(ui)).texts
    expect(t.split('|').at(-1)).toBe('Esc close')
    await ui.press({ key: 'tab-home' })
    expect((await paneText(ui)).texts).toContain('NEXT STEP')
  })
})

describe('Home', () => {
  test('NEXT STEP: Enter puts the command in the prompt, r runs it now from a timer; both close the pane', async ($, on) => {
    const { clock, seen } = world(on, ready())
    await start($, clock)
    const ui = await pane($)
    const { texts } = await paneText(ui)
    expect(texts).toContain('NEXT STEP|❯ /impl feat/demo:Ticket t1|One ticket is ready.')
    expect(texts.split('|').at(-1)).toBe('Enter put in prompt · r run now · Esc close')
    expect((await ui.find({ key: 'next' }))?.props.autoFocus).toBe(true)
    await ui.press({ key: 'next' })
    expect([seen.fills, seen.closes]).toEqual([['/impl feat/demo:Ticket t1'], ['plugin']])
    await ui.press({ key: 'run' })
    expect(seen.closes).toEqual(['plugin', 'plugin'])
    expect(seen.commands).toEqual([])
    await clock.advance(100)
    expect(seen.commands).toEqual(['/impl feat/demo:Ticket t1'])
    expect(seen.fills).toEqual(['/impl feat/demo:Ticket t1'])
  })

  test('MY CLAIMS: selecting reveals renew and release; release needs a second x; only on press', async ($, on) => {
    const { clock, seen } = world(on, holding(20))
    await start($, clock)
    await clock.advance(15000)
    expect(seen.runs.some(r => /claims (renew|release|acquire)/.test(r.argv))).toBe(false)
    const ui = await pane($)
    let { texts, buttons } = await paneText(ui)
    expect(texts).toContain('MY CLAIMS|◷ Ticket t1|  19m')
    expect(buttons).not.toContain('renew-t1')
    expect((await ui.find({ key: 'claim-t1' }))?.props.hotkey).toBe('1')
    await ui.press({ key: 'claim-t1' })
    ;({ texts, buttons } = await paneText(ui))
    expect(buttons).toEqual(expect.arrayContaining(['renew-t1', 'release-t1']))
    expect(buttons).not.toContain('run')
    expect(texts.split('|').at(-1)).toBe('r renew · x release · Esc close')
    await ui.press({ key: 'release-t1' })
    expect(seen.runs.some(r => r.argv.includes('claims release'))).toBe(false)
    expect((await ui.find({ key: 'release-t1' }))?.text).toBe('press x again to release')
    await ui.press({ key: 'release-t1' })
    expect(seen.runs.filter(r => r.argv.includes('claims release')).map(r => r.argv)).toEqual([`${T} claims release t1`])
    expect(seen.toasts.at(-1)).toBe('lit: claim released')
  })

  test('renew from MY CLAIMS renews for 30 minutes; a failure shows once as a toast and a red ⚠ row until the next good refresh', async ($, on) => {
    const w: World = { ...holding(20), renewFails: 'claim expired' }
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'claim-t1' })
    await ui.press({ key: 'renew-t1' })
    expect(seen.runs.filter(r => r.argv.includes('claims renew')).map(r => r.argv)).toEqual([`${T} claims renew t1 --for 30m`])
    expect(seen.toasts.filter(t => t.includes('failed'))).toEqual(['lit: renew failed: claim expired'])
    const row = await ui.find({ type: 'Text', text: /^⚠/ })
    expect([row?.text, row?.props.color]).toEqual(['⚠ renew failed: claim expired', 'error'])
    await clock.advance(5000)
    expect(await ui.find({ type: 'Text', text: /^⚠/ })).toBeUndefined()
    w.renewFails = undefined
    await ui.press({ key: 'claim-t1' })
    await ui.press({ key: 'renew-t1' })
    expect(seen.toasts.at(-1)).toBe('lit: claim renewed for 30m')
    expect(await ui.find({ type: 'Text', text: /^⚠/ })).toBeUndefined()
  })

  test('WAITING FOR YOU: a digit puts that item\'s command in the prompt and closes the pane', async ($, on) => {
    const w = ready()
    issues(w, ticket('t1'), { ...ticket('d1'), title: 'Pick a database', labels: ['decision'] }, { ...ticket('q1'), title: 'Copy', labels: ['ready-for-human'] })
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    expect((await paneText(ui)).texts).toContain('WAITING FOR YOU|⚑ |Pick a database|  decision|⚑ |Copy|  ready-for-human')
    expect((await ui.find({ key: 'wait-d1' }))?.props.hotkey).toBe('1')
    await ui.press({ key: 'wait-q1' })
    expect(seen.fills).toEqual(['Show me the lit issue "Copy" (q1) in feat/demo and what it needs from me.'])
    expect(seen.closes).toEqual(['plugin'])
  })
})

describe('Launch', () => {
  test('a target step: the command once, back to the list; Enter fills; r runs now', async ($, on) => {
    const { clock, seen } = world(on, ready())
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-launch' })
    let { texts, buttons } = await paneText(ui)
    expect(buttons.filter(b => b.startsWith('flow-'))).toEqual(['flow-planner', 'flow-to-spec', 'flow-impl'])
    expect(texts).toContain('5: orchestrate run ready tickets in parallel|  needs 2+ ready tickets')
    expect(texts).toContain('impl        |implement one ready ticket|  ★ recommended')
    await ui.press({ key: 'flow-impl' })
    ;({ texts, buttons } = await paneText(ui))
    expect(texts).toContain('IMPLEMENT WHICH TICKET?')
    expect(texts).toContain('Ticket t2|  blocked by Ticket t1')
    expect(texts.split('→').length).toBe(2)
    expect(texts).toContain('→ /impl feat/demo:Ticket t1')
    expect(texts.split('|').at(-1)).toBe('Enter put in prompt · r run now · 1–2 choose · Esc back')
    expect(buttons).not.toContain('target-1')
    await ui.press({ key: 'back' })
    expect((await paneText(ui)).texts).toContain('START A WORKFLOW')
    await ui.press({ key: 'flow-impl' })
    await ui.press({ key: 'put' })
    expect([seen.fills, seen.closes]).toEqual([['/impl feat/demo:Ticket t1'], ['plugin']])
    await ui.press({ key: 'tab-launch' })
    await ui.press({ key: 'flow-to-spec' })
    expect((await paneText(ui)).texts).toContain('WRITE THE SHARED SPECIFICATION|→ /to-spec feat/demo')
    await ui.press({ key: 'run' })
    await clock.advance(100)
    expect(seen.commands).toEqual(['/to-spec feat/demo'])
  })

  test('planner targets a decision; orchestrate never offers run now', async ($, on) => {
    const w = ready()
    issues(w, ticket('t1'), ticket('t3'), { ...ticket('d1'), title: 'Pick a database', labels: ['decision'] })
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    await ui.press({ key: 'tab-launch' })
    await ui.press({ key: 'flow-planner' })
    await ui.press({ key: 'target-1' })
    expect((await paneText(ui)).texts).toContain('→ /planner feat/demo')
    await ui.press({ key: 'target-0' })
    expect((await paneText(ui)).texts).toContain('→ /planner feat/demo decision: Pick a database')
    await ui.press({ key: 'back' })
    await ui.press({ key: 'flow-orchestrate' })
    expect((await paneText(ui)).buttons).not.toContain('run')
    await ui.press({ key: 'put' })
    expect(seen.fills).toEqual(['/orchestrate feat/demo'])
  })

  test('below 60 columns the right-hand meta drops and the tabs go under the title', async ($, on) => {
    const { clock } = world(on, ready())
    await start($, clock)
    const ui = await pane($, 50)
    await ui.press({ key: 'tab-launch' })
    const { texts } = await paneText(ui)
    expect(texts).not.toContain('recommended')
    expect((await ui.find({ key: 'head' }))?.props.flexDirection).toBe('column')
  })

  test('outside ready the pane shows the state and its one action', async ($, on) => {
    const w = ready()
    w.meta = 'down'
    const { clock, seen } = world(on, w)
    await start($, clock)
    const ui = await pane($)
    expect((await paneText(ui)).texts).toBe('⚫ lit · tracker not answering · 127.0.0.1:7472|Enter retry · Esc close')
    const before = seen.runs.length
    await ui.press({ key: 'fix' })
    expect(seen.runs.length).toBeGreaterThan(before)
  })
})
