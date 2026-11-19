// The mod as the engine runs it: its hooks, with lit, the tracker and the files answered by the
// test beneath it. Each test starts a session, lets the first refresh run and reads the band.
import { describe, expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const NOW = Date.parse('2026-11-20T00:00:00Z')
const SID = 'sid-1'
const ASSOC = { local_session: 'agent-1', endpoint: 'http://127.0.0.1:7472', client_id: 'me', project_id: 'p1', status: 'ready' }
const T = 'lit --session agent-1 --endpoint http://127.0.0.1:7472 --format json'
const items = (...xs: unknown[]) => JSON.stringify({ items: xs })
const out = (exitCode: number, stdout: string, stderr = '') => ({ value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false } })
const minutes = (m: number) => new Date(NOW + m * 60000).toISOString()
const myClaim = (m: number) => ({ issue_id: 't1', owner_client_id: 'me', owner_actor: { kind: 'agent', name: 'claude' }, token: 'TOKEN-1', expires_at: minutes(m) })
const ticket = (id: string, extra: object = {}) =>
  ({ id, title: `Ticket ${id}`, state: 'open', labels: ['ready-for-agent'], blocked_by: [], claim: null, created_at: `2026-11-19T00:00:0${id.length}Z`, ...extra })

type World = {
  show: { exitCode: number; stdout: string }
  meta: number | 'down'
  claimMinutes: number | null
  config: string | null
  lit: Record<string, { exitCode?: number; stdout: string }>
}
const ready = (): World => ({
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

// Answers everything the mod asks the engine for; records what it ran, wrote, filled and toasted.
function world(on: On, w: World) {
  const clock = mock.clock(on, { now: NOW })
  mock.env(on, { HOME: '/home/u', LIT_WORKFLOW_STATE_DIR: '/wf' })
  const seen = { runs: [] as { argv: string; cwd: string | undefined }[], files: {} as Record<string, string>, fills: [] as string[], toasts: [] as string[] }
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
      w.claimMinutes = 30
      return out(0, items({ issue_id: 't1', claim: myClaim(30) }))
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
  return { clock, seen }
}

const BAND = { plugin: 'lit-ui', surface: 'terminal', component: 'AbovePrompt' } as const
const bandProps = (isWorking = false) =>
  ({ hasSurvey: false, isWorking, maxRows: 4, bodyColumns: 120, scroll: { offset: 0, bodyRows: 4 }, view: {} })
async function start($: any, clock: { settle: () => Promise<void> }) {
  await $.session.start({ cwd: '/checkout', surface: 'terminal', isInteractive: true })
  await clock.settle()
}
async function bandText($: any, isWorking = false) {
  const ui = await $.ui.mount({ ...BAND, props: bandProps(isWorking) })
  const texts = (await ui.findAll({ type: 'Text' })).map((t: { text: string }) => t.text).join('')
  const buttons = (await ui.findAll({ type: 'Button' })).map((b: { text: string }) => b.text)
  return { ui, texts, buttons }
}

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
    expect(seen.toasts).toEqual(['lit: claim on "Ticket t1" expires in 4m'])
    expect(seen.runs.some(r => /claims (renew|acquire|release)/.test(r.argv))).toBe(false)
    const { ui, texts, buttons } = await bandText($)
    expect(texts).toBe('◷ Ticket t1 3m left · next /impl')
    expect(buttons).toEqual(['renew', 'lit'])
    await ui.press({ key: 'band-3' })
    expect(seen.runs.filter(r => r.argv.includes('claims renew')).map(r => r.argv)).toEqual([`${T} claims renew t1 --for 30m`])
    expect(seen.toasts.at(-1)).toBe('lit: claim renewed for 30m')
    expect((await bandText($)).buttons).toEqual(['impl', 'lit'])
  })

  test('an expired claim toasts once', async ($, on) => {
    const w: World = { ...ready(), claimMinutes: 0.05 }
    const { clock, seen } = world(on, w)
    await start($, clock)
    w.claimMinutes = null
    await clock.advance(10000)
    expect(seen.toasts).toEqual(['lit: claim on "Ticket t1" expires in 3s', 'lit: claim on "Ticket t1" has expired'])
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
    expect(seen.toasts.at(-1)).toContain('the lit pane is not part of this version yet')
    w.show = { exitCode: 0, stdout: JSON.stringify({ ...ASSOC, status: 'pending' }) }
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['○ lit · connection pending', ['reconcile']])
    await b.ui.press({ key: 'band-1' })
    expect(seen.runs.some(r => r.argv === `lit workflow-session --host claude --runtime-id ${SID} reconcile`)).toBe(true)
    expect(seen.toasts.at(-1)).toMatch(/^lit: reconcile failed: /)
    w.show = { exitCode: 3, stdout: '{"error":{"code":"association_not_found","message":"none"}}' }
    b = await now()
    expect([b.texts, b.buttons]).toEqual(['○ lit · not connected in this conversation', ['connect']])
    await b.ui.press({ key: 'band-1' })
    expect(seen.runs.some(r => r.argv === `lit workflow-session --host claude --runtime-id ${SID} fresh`)).toBe(true)
    expect(seen.toasts.at(-1)).toMatch(/^lit: connect failed: /)
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
