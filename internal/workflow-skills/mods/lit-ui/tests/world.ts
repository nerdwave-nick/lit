// The world beneath the mod in the session tests: lit, the tracker, the files, the prompt and
// the panes, answered by the test and recorded.
import { mock } from 'claude-code/testing'
import type { On } from 'claude-code'

export const NOW = Date.parse('2026-11-20T00:00:00Z')
export const SID = 'sid-1'
export const ASSOC = { local_session: 'agent-1', endpoint: 'http://127.0.0.1:7472', client_id: 'me', project_id: 'p1', status: 'ready' }
export const T = 'lit --session agent-1 --endpoint http://127.0.0.1:7472 --format json'
export const items = (...xs: unknown[]) => JSON.stringify({ items: xs })
export const out = (exitCode: number, stdout: string, stderr = '') => ({ value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false } })
export const minutes = (m: number) => new Date(NOW + m * 60000).toISOString()
export const myClaim = (m: number) => ({ issue_id: 't1', owner_client_id: 'me', owner_actor: { kind: 'agent', name: 'claude' }, token: 'TOKEN-1', expires_at: minutes(m) })
export const ticket = (id: string, extra: object = {}) =>
  ({ id, title: `Ticket ${id}`, state: 'open', labels: ['ready-for-agent'], blocked_by: [], claim: null, created_at: `2026-11-19T00:00:0${id.length}Z`, ...extra })

export type World = {
  show: { exitCode: number; stdout: string }
  meta: number | 'down'
  claimMinutes: number | null
  config: string | null
  lit: Record<string, { exitCode?: number; stdout: string }>
  // Why `claims renew` fails, when it does.
  renewFails?: string
}
export const ready = (): World => ({
  show: { exitCode: 0, stdout: JSON.stringify(ASSOC) },
  meta: 200,
  claimMinutes: null,
  config: null,
  lit: {
    [`${T} session get`]: { stdout: items({ project_id: 'p1', stage: 'impl' }) },
    [`${T} projects list --all`]: { stdout: items({ id: 'p1', title: 'feat/demo', description: '# D', repository_refs: [] }) },
    [`${T} issues list --all --project p1`]: { stdout: items(ticket('t1'), ticket('t2', { blocked_by: ['t1'] })) },
    [`${T} milestones list --all --project p1`]: { stdout: items() },
  },
})

// Answers everything the mod asks the engine for; records what it ran, wrote, filled, toasted,
// opened, closed and ran as a command. A plugin's $.ui.focus never reaches the test's hooks in the
// kit, so pane-acts.test.ts covers where the focus goes.
export function world(on: On, w: World) {
  const clock = mock.clock(on, { now: NOW })
  mock.env(on, { HOME: '/home/u', LIT_WORKFLOW_STATE_DIR: '/wf' })
  const seen = {
    runs: [] as { argv: string; cwd: string | undefined }[], files: {} as Record<string, string>, fills: [] as string[], toasts: [] as string[],
    opens: [] as object[], closes: [] as string[], commands: [] as string[],
  }
  on('session.start', async ($, e) => ({ cwd: e.cwd }))
  on('session.id', async () => ({ value: SID }))
  on('command.register', async ($, e) => ({ value: { command: e.name } }))
  on('process.run', async ($, e) => {
    const argv = e.argv.join(' ')
    seen.runs.push({ argv, cwd: e.init?.cwd })
    if (argv === 'uname -s') return out(0, 'Linux\n')
    if (argv.endsWith(`--runtime-id ${SID} show`)) return out(w.show.exitCode, w.show.stdout)
    if (argv === `${T} claims list --all`) {
      const claims = w.claimMinutes === null ? [] : [myClaim(w.claimMinutes)]
      return out(0, items(...claims))
    }
    if (argv.startsWith(`${T} claims renew t1`)) {
      if (w.renewFails) return out(1, JSON.stringify({ error: { code: 'claim_expired', message: w.renewFails } }))
      w.claimMinutes = 30
      return out(0, items({ issue_id: 't1', claim: myClaim(30) }))
    }
    if (argv === `${T} claims release t1`) {
      w.claimMinutes = null
      return out(0, items({ issue_id: 't1' }))
    }
    const a = w.lit[argv]
    return a ? out(a.exitCode ?? 0, a.stdout) : out(1, '', `unexpected ${argv}`)
  })
  on('http.fetch', async ($, e) => {
    if (w.meta === 'down') return { deny: 'connection refused' }
    return { value: { status: w.meta, ok: w.meta === 200, headers: {}, text: JSON.stringify({ data: { title_prefixes: ['feat'] } }) } }
  })
  on('fs.exists', async ($, e) => ({ value: e.path.endsWith('/config.json') && w.config !== null }))
  on('fs.read', async ($, e) => (e.path.endsWith('/config.json') && w.config !== null ? { value: w.config } : { deny: 'missing' }))
  on('fs.write', async ($, e) => {
    seen.files[e.path] = e.text
    return { value: undefined }
  })
  on('prompt.fill', async ($, e) => {
    seen.fills.push(e.text)
    return { isFilled: true }
  })
  on('ui.toast', async ($, e) => {
    seen.toasts.push(e.text)
    return { value: undefined }
  })
  on('ui.open', async ($, e) => {
    seen.opens.push(e)
    return { value: { isPlaced: true } }
  })
  on('ui.close', async ($, e) => {
    seen.closes.push(e.origin.kind)
    return { value: undefined }
  })
  on('command.run', async ($, e) => {
    seen.commands.push(`/${e.command} ${e.args}`)
    return { text: '' }
  })
  return { clock, seen }
}

export const BAND = { plugin: 'lit-ui', surface: 'terminal', component: 'AbovePrompt' } as const
export const bandProps = (isWorking = false) =>
  ({ hasSurvey: false, isWorking, maxRows: 4, bodyColumns: 120, scroll: { offset: 0, bodyRows: 4 }, view: {} })
export async function start($: any, clock: { settle: () => Promise<void> }) {
  await $.session.start({ cwd: '/checkout', surface: 'terminal', isInteractive: true })
  await clock.settle()
}
export async function bandText($: any, isWorking = false) {
  const ui = await $.ui.mount({ ...BAND, props: bandProps(isWorking) })
  const texts = (await ui.findAll({ type: 'Text' })).map((t: { text: string }) => t.text).join('')
  const buttons = (await ui.findAll({ type: 'Button' })).map((b: { text: string }) => b.text)
  return { ui, texts, buttons }
}


export const PANE = { plugin: 'lit-ui', surface: 'terminal', component: 'Pane', requestId: 'lit-ui' } as const
export const paneProps = (bodyColumns = 100) =>
  ({ title: 'lit', isFocused: true, bodyColumns, placement: 'inline' as const, scroll: { offset: 0, bodyRows: 30 }, view: {} })
// The pane as drawn: its text row by row is not kept, so the texts are joined with '|'.
export async function paneText(ui: any): Promise<{ texts: string; buttons: string[] }> {
  const texts = (await ui.findAll({ type: 'Text' })).map((t: { text: string }) => t.text).join('|')
  const buttons = (await ui.findAll({ type: 'Button' })).map((b: { key: string }) => b.key)
  return { texts, buttons }
}
