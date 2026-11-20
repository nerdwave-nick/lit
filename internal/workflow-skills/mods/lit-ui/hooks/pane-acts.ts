// What the pane's presses do. The engine calls come in as a PaneIo built in register.tsx (the
// engine follows `$` within one file only); this file decides what follows a press.
import type { BandAction } from './band'
import { slash } from './pane'
import type { Flow, PaneUi, Tab, View } from '../types'

export type PaneIo = {
  view: () => Promise<View | null>
  ui: () => Promise<PaneUi>
  patch: (fn: (ui: PaneUi) => PaneUi) => Promise<void>
  // Moves the pane's focus ring onto the element keyed `key`; a refusal is ignored.
  focus: (key: string) => void
  close: () => Promise<void>
  fill: (text: string) => Promise<void>
  // Runs `/command args` from a timer, never from inside a command.run hook.
  runLater: (command: string, args: string) => void
  renew: (issueId: string) => Promise<void>
  release: (issueId: string) => Promise<void>
  press: (does: BandAction) => void
}

export type PaneActs = {
  tab: (tab: Tab) => void
  fill: (text: string) => void
  run: (text: string) => void
  selectClaim: (issueId: string) => void
  renew: (issueId: string) => void
  release: (issueId: string) => void
  pickFlow: (flow: Flow) => void
  choose: (sel: number) => void
  back: () => void
  // Esc from the person: true when it went back from a target step, so the pane stays open.
  escape: () => Promise<boolean>
  fix: (does: BandAction) => void
}

const settled = (ui: PaneUi): PaneUi => ({ ...ui, step: null, claim: null, confirm: null })

export function paneActs(io: PaneIo): PaneActs {
  // Launching leaves the pane, so the cursor is where the next keystroke (Enter) belongs.
  const leave = async (text: string, mode: 'fill' | 'run') => {
    await io.patch(settled)
    await io.close()
    const command = slash(text)
    if (mode === 'run' && command) io.runLater(command.command, command.args)
    else await io.fill(text)
  }
  const tab = async (to: Tab) => {
    await io.patch(ui => ({ ...settled(ui), tab: to }))
    // The ring goes where Enter acts: the next step on Home, the recommended workflow on Launch.
    if (to === 'home') io.focus('next')
    const flow = to === 'launch' ? (await io.view())?.recommend.flow : null
    if (flow) io.focus(`flow-${flow}`)
  }
  const release = async (issueId: string) => {
    if ((await io.ui()).confirm !== issueId) return io.patch(ui => ({ ...ui, confirm: issueId }))
    await io.patch(settled)
    await io.release(issueId)
  }
  return {
    tab: to => void tab(to),
    fill: text => void leave(text, 'fill'),
    run: text => void leave(text, 'run'),
    selectClaim: issueId => void io.patch(ui => ({ ...ui, claim: ui.claim === issueId ? null : issueId, confirm: null }))
      .then(() => io.focus(`renew-${issueId}`)),
    renew: issueId => void io.patch(settled).then(() => io.renew(issueId)),
    release: issueId => void release(issueId),
    pickFlow: flow => void io.patch(ui => ({ ...ui, step: flow, sel: 0 })).then(() => io.focus('put')),
    choose: sel => void io.patch(ui => ({ ...ui, sel })).then(() => io.focus('put')),
    back: () => void io.patch(ui => ({ ...ui, step: null })),
    escape: async () => {
      if (!(await io.ui()).step) return false
      await io.patch(ui => ({ ...ui, step: null }))
      return true
    },
    fix: does => io.press(does),
  }
}
