// The lit-ui mod: lit's state in a band above the prompt, the lit pane, claim toasts and a
// per-session status feed. All tracker behavior stays in lit commands; the mod reads, presents
// and calls them.
// Every call on `$` is in this file: the engine follows `$` within one file only.
import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import { band } from './band'
import type { BandAction } from './band'
import { BandView } from './band-view'
import { CONFIG_FILE, invokesLit, parseConfig, workflowStateDir } from './config'
import { errorText, gather, readAssoc, trackerArgv } from './data'
import type { Io } from './data'
import { buildFeed, feedPath } from './feed'
import { refreshGate } from './gate'
import { derive } from './model'
import { claimNotices } from './notices'
import type { Watched } from './notices'
import { EMPTY_UI, PANE_ID } from './pane'
import { paneActs } from './pane-acts'
import type { PaneActs } from './pane-acts'
import { PaneView } from './pane-view'
import { stageReading, suggestionShown, watchStage } from './suggest'
import type { StageWatch } from './suggest'
import type { Config, PaneUi } from '../types'

const POLL_MS = 5000
const CALL_TIMEOUT_MS = 3000
const ADAPTER_TIMEOUT_MS = 15000
const META_TIMEOUT_MS = 2000
const RENEW_FOR = '30m'
// lit's own default endpoint, which a connect without --endpoint also falls back to.
const DEFAULT_ENDPOINT = 'http://127.0.0.1:7411'
// Run now waits for the press to settle, then runs the command from a timer.
const RUN_DELAY_MS = 50

const viewAtom = atom({ plugin: 'lit-ui', key: 'view' } as const, null)
const refreshAtom = atom({ plugin: 'lit-ui', key: 'refresh' } as const, null)
const uiAtom = atom({ plugin: 'lit-ui', key: 'ui' } as const, EMPTY_UI)
const patchUi = ($: EngineInterface, fn: (ui: PaneUi) => PaneUi) => update($, uiAtom, fn)

// Module state, fresh on every load. config.json is read once per load: setup rewrites it
// only together with a reinstall.
let config: Config | null = null
let stateDir: string | null | undefined
let cwdReady = false
let watched: Watched = {}
let watchedSid = ''
let feedProblem: string | null = null
let lastFailure: string | null = null
let stageWatch: StageWatch = { seen: null, pending: null }

async function settings($: EngineInterface): Promise<{ config: Config; stateDir: string | null; cwd: string }> {
  if (!config) {
    const path = `${$.plugin.root}/${CONFIG_FILE}`
    config = parseConfig((await $.fs.exists(path)) ? await $.fs.read(path) : null)
  }
  if (stateDir === undefined) {
    const uname = await $.process.run(['uname', '-s'], { timeoutMs: CALL_TIMEOUT_MS }).catch(() => null)
    stateDir = workflowStateDir({
      override: await $.env.get('LIT_WORKFLOW_STATE_DIR'),
      xdg: await $.env.get('XDG_STATE_HOME'),
      home: await $.env.get('HOME'),
      localAppData: await $.env.get('LOCALAPPDATA'),
    }, uname?.exitCode === 0 ? uname.stdout.trim() : null)
  }
  // lit writes a cache into its working directory, so it runs in one of the mod's own.
  const cwd = stateDir ? `${stateDir}/lit-ui` : '/'
  if (stateDir && !cwdReady) {
    // A directory that cannot be made shows as failed lit commands in the state.
    await $.fs.write(`${cwd}/README.txt`, 'Working directory of the lit-ui mod\'s lit commands.\n').catch(() => {})
    cwdReady = true
  }
  return { config, stateDir, cwd }
}

const io = ($: EngineInterface, cwd: string, timeoutMs = CALL_TIMEOUT_MS): Io => ({
  run: argv => $.process.run(argv, { cwd, timeoutMs }),
  meta: url => new Promise((resolve, reject) => {
    const timer = $.clock.after(META_TIMEOUT_MS, () => reject(new Error('timed out')))
    $.http.fetch(url).then(r => {
      timer.cancel()
      resolve({ status: r.status, text: r.text })
    }, reject)
  }),
})

async function refreshNow($: EngineInterface): Promise<void> {
  const started = await $.clock.now()
  const s = await settings($)
  const sid = await $.session.id()
  const defaultEndpoint = (await $.env.get('LIT_ENDPOINT')) || DEFAULT_ENDPOINT
  const raw = await gather(io($, s.cwd), { sid, lit: s.config.lit, defaultEndpoint })
  if (s.config.problem) raw.errors.unshift(s.config.problem)
  if (!s.stateDir) raw.errors.push('lit\'s workflow state directory is unknown: no status feed')
  const now = await $.clock.now()
  const view = derive(raw, now)
  await update($, viewAtom, () => view)
  // /clear starts a conversation with another ID: its claims are watched afresh.
  if (sid !== watchedSid) [watched, watchedSid] = [{}, sid]
  const notices = claimNotices(watched, raw.claims === null ? null : view.ownClaims, now)
  watched = notices.watched
  for (const text of notices.toasts) $.ui.toast(text)
  stageWatch = watchStage(stageWatch, stageReading(raw, view))
  void suggestNextStage($)
  if (s.stateDir) {
    feedProblem = await $.fs.write(feedPath(s.stateDir, sid), JSON.stringify(buildFeed(view, now), null, 2) + '\n')
      .then(() => null, (err: Error) => `status feed: ${err.message}`)
  }
  await update($, refreshAtom, () => ({ at: new Date(now).toISOString(), ms: now - started, session: raw.assoc?.local_session ?? null }))
}

// Offers the next stage's command as ghost text until the prompt box has shown it once.
async function suggestNextStage($: EngineInterface): Promise<void> {
  const text = stageWatch.pending
  if (!text) return
  const r = await $.prompt.suggest({ text }).catch(() => null)
  if (r?.isShown) stageWatch = suggestionShown(stageWatch, text)
}

const gate = refreshGate()
// Refreshes now, or once more after the refresh already running.
async function refresh($: EngineInterface): Promise<void> {
  const wait = gate.enter()
  if (wait) return wait
  do {
    try {
      await refreshNow($)
      lastFailure = null
      // A failed action's row stays until the next successful refresh.
      if ((await read($, uiAtom)).failure) await patchUi($, ui => ({ ...ui, failure: null }))
    } catch (err) {
      // A failure that persists is said once, not at every poll.
      const message = `lit: refresh failed: ${(err as Error).message}`
      if (message !== lastFailure) $.ui.toast(message)
      lastFailure = message
    }
  } while (gate.again())
}

// A human's press: run one lit command, say how it went, then refresh. A failure also shows in
// the pane until the next successful refresh.
async function act($: EngineInterface, what: string, argv: (lit: string, sid: string) => Promise<string[]>, ok: string) {
  let failure: string | null = null
  try {
    const s = await settings($)
    const r = await io($, s.cwd, ADAPTER_TIMEOUT_MS).run(await argv(s.config.lit, await $.session.id()))
    if (r.exitCode !== 0) throw new Error(errorText(r))
    $.ui.toast(`lit: ${ok}`)
  } catch (err) {
    failure = `${what} failed: ${(err as Error).message}`
    $.ui.toast(`lit: ${failure}`)
  }
  await refresh($)
  if (failure) await patchUi($, ui => ({ ...ui, failure }))
}

// Renews or releases only this session's own claim, only on the person's press.
const ownClaim = ($: EngineInterface, what: 'renew' | 'release', args: string[], ok: string) => act($, what, async (lit, sid) => {
  const assoc = await readAssoc(io($, (await settings($)).cwd), lit, sid)
  if (!assoc) throw new Error('not connected in this conversation')
  return trackerArgv(lit, assoc, ['claims', what, ...args])
}, ok)
const renew = ($: EngineInterface, issueId: string) => ownClaim($, 'renew', [issueId, '--for', RENEW_FOR], `claim renewed for ${RENEW_FOR}`)
const release = ($: EngineInterface, issueId: string) => ownClaim($, 'release', [issueId], 'claim released')

const adapter = ($: EngineInterface, action: 'fresh' | 'reconcile', ok: string) =>
  act($, action === 'fresh' ? 'connect' : action, async (lit, sid) => [lit, 'workflow-session', '--host', 'claude', '--runtime-id', sid, action], ok)

function press($: EngineInterface, does: BandAction): void {
  switch (does.kind) {
    case 'fill':
      void $.prompt.fill({ text: does.command }).then(() => refresh($))
      return
    case 'renew': return void renew($, does.issueId)
    case 'connect': return void adapter($, 'fresh', 'connected')
    case 'reconcile': return void adapter($, 'reconcile', 'reconciled')
    case 'retry': return void refresh($)
    case 'pane': return openPane($)
  }
}

// Opened synchronously inside the press or command: after an await the engine no longer takes
// it as the person's request and below 144 columns leaves it unplaced.
const OPEN = { id: PANE_ID, title: 'lit', focus: true, closeOnEscape: true } as const
function openPane($: EngineInterface): void {
  void $.ui.open(OPEN).then(r => r.isPlaced || $.ui.toast('lit: widen the terminal to see the lit pane'))
  void patchUi($, () => EMPTY_UI)
}

const acts = ($: EngineInterface): PaneActs => paneActs({
  view: () => read($, viewAtom),
  ui: () => read($, uiAtom),
  patch: fn => patchUi($, fn).then(() => {}),
  focus: key => void $.ui.focus({ requestId: PANE_ID, key }).catch(() => {}),
  close: () => $.ui.close({ id: PANE_ID }).catch(() => {}),
  fill: text => $.prompt.fill({ text }).then(() => refresh($)),
  runLater: (command, args) => void $.clock.after(RUN_DELAY_MS, () =>
    void $.command.run({ command, args }).catch((err: Error) => $.ui.toast(`lit: /${command} failed: ${err.message}`))),
  renew: issueId => renew($, issueId),
  release: issueId => release($, issueId),
  press: does => press($, does),
})

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({ name: 'lit-ui', description: 'lit: open the lit pane; /lit-ui debug shows the endpoint, status mode, session and last refresh' })
    await $.command.register({ name: 'lit-refresh', description: 'lit: refresh the band and the status feed now' })
    $.clock.every(POLL_MS, () => void refresh($))
    void refresh($)
    return next(e)
  })

  on('command.run', { command: 'lit-refresh' }, async $ => {
    await refresh($)
    const v = await read($, viewAtom)
    return { text: `lit refreshed: ${v?.state ?? 'unknown'}` }
  })

  on('command.run', { command: 'lit-ui' }, async ($, e) => {
    if (e.args.trim() !== 'debug') {
      openPane($)
      return { text: 'lit pane opened' }
    }
    const [v, r, s] = [await read($, viewAtom), await read($, refreshAtom), await settings($)]
    const lines = [
      `endpoint: ${v?.endpoint ?? 'unknown'}`,
      `status mode: ${s.config.mode} (${s.config.source === 'file' ? CONFIG_FILE : 'development default'}; lit: ${s.config.lit})`,
      `lit session: ${r?.session ?? 'none'}`,
      `state: ${v?.state ?? 'unknown'}`,
      `stage: ${v?.stage ?? 'none'}`,
      `last refresh: ${r ? `${r.at}, ${r.ms} ms` : 'never'}`,
      ...[...(v?.errors ?? []), ...(feedProblem ? [feedProblem] : [])].map(err => `error: ${err}`),
    ]
    return { text: lines.join('\n') }
  })

  // Refresh right after the agent runs lit, instead of waiting for the next poll.
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    const result = await next(e)
    if (invokesLit(e.command)) void refresh($)
    return result
  }).catch(($, e, next) => next(e))

  // Esc in a Launch target step goes back to the list instead of closing the pane.
  on('ui.close', async ($, e, next) => {
    if (e.id !== PANE_ID || e.origin.kind !== 'person' || !(await acts($).escape())) return next(e)
    // Esc handed the keyboard back to the prompt; asking for it again keeps the pane usable.
    $.clock.after(0, () => void $.ui.open(OPEN))
    return { value: undefined }
  })

  on('ui.render', { component: 'Pane', requestId: PANE_ID }, async ($, e) => {
    const [view, ui] = [await read($, viewAtom), await read($, uiAtom)]
    return PaneView($.ui.resolve(e), view, ui, config?.mode ?? 'both', acts($), e.props.bodyColumns)
  })

  // A stage cleared during a turn is suggested when the turn ends; no other turn end suggests.
  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    if (!e.agentId) void suggestNextStage($)
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const view = await read($, viewAtom)
    if (e.props.hasSurvey || !view) return next(e)
    return BandView($.ui.resolve(e), band(view, config?.mode ?? 'both'), e.props.isWorking, does => press($, does))
  })
}
