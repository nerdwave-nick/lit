// What the pane's shell, Home and Launch views show, derived from the view and the pane's own
// state. Pure (no `$`), so it is unit-tested; pane-view.tsx draws it, pane-acts.ts acts on it.
import { WARN_MS } from './band'
import { duration, launchText } from './model'
import type { ClaimView, Flow, PaneUi, Tab, View, Waiting } from '../types'

export const PANE_ID = 'lit-ui'
// Below this many body columns the right-hand meta drops and the tabs go under the title.
export const NARROW = 60
export const TABS: readonly { tab: Tab; key: string; label: string }[] = [
  { tab: 'home', key: 'h', label: 'Home' },
  { tab: 'launch', key: 'l', label: 'Launch' },
  { tab: 'board', key: 'b', label: 'Board' },
  { tab: 'project', key: 'p', label: 'Project' },
]
export const EMPTY_UI: PaneUi = { tab: 'home', step: null, sel: 0, claim: null, confirm: null, failure: null }

export type PaneTone = 'text' | 'dim' | 'faint' | 'accent' | 'success' | 'warning' | 'error'

// Without a project only the Project view has something to show.
export const tabOf = (v: View, ui: PaneUi): Tab => (v.state === 'noproject' ? 'project' : ui.tab)

// Orchestrate always goes through the prompt: no run now.
export const runsNow = (flow: Flow | null) => flow !== null && flow !== 'orchestrate'

// `/impl P:T` as the command and its arguments; null for a plain prompt.
export function slash(text: string): { command: string; args: string } | null {
  const m = /^\/(\S+)\s*(.*)$/s.exec(text)
  return m ? { command: m[1]!, args: m[2]! } : null
}

// Digit hotkeys reach the first nine rows; later rows are reached with Tab and Enter.
export const digit = (n: number) => (n >= 1 && n <= 9 ? String(n) : undefined)
const choose = (n: number) => (n === 0 ? null : n === 1 ? '1 choose' : `1–${Math.min(n, 9)} choose`)
const hints = (...parts: (string | null | false)[]) => parts.filter(Boolean).join(' · ')

export type HomeModel = {
  next: { command: string | null; reason: string; runNow: boolean }
  claims: { n: number; claim: ClaimView; warn: boolean; selected: boolean }[]
  waiting: { n: number; item: Waiting }[]
  hints: string
}

// NEXT STEP, then MY CLAIMS (this session's only), then WAITING FOR YOU, numbered in that order.
export function home(v: View, ui: PaneUi): HomeModel {
  const selected = v.ownClaims.some(c => c.issueId === ui.claim) ? ui.claim : null
  const { command, reason, flow } = v.recommend
  const claims = v.ownClaims.map((claim, i) =>
    ({ n: i + 1, claim, warn: claim.remainingMs < WARN_MS, selected: claim.issueId === selected }))
  const waiting = v.waiting.map((item, i) => ({ n: claims.length + i + 1, item }))
  // While a claim is selected, r renews it.
  const runNow = command !== null && runsNow(flow) && selected === null
  const text = selected !== null
    ? hints(ui.confirm === selected ? 'x again to release' : 'r renew · x release', ui.confirm === selected && 'r renew', 'Esc close')
    : hints(command !== null && 'Enter put in prompt', runNow && 'r run now', choose(claims.length + waiting.length), 'Esc close')
  return { next: { command, reason, runNow }, claims, waiting, hints: text }
}

const DESCRIBE: Record<Flow, string> = {
  planner: 'settle open decisions',
  'to-spec': 'write the shared specification',
  'to-tickets': 'cut implementation tickets',
  impl: 'implement one ready ticket',
  orchestrate: 'run ready tickets in parallel',
}
export const FLOWS = Object.keys(DESCRIBE) as Flow[]

export type FlowRow = { flow: Flow; describe: string; meta: string; enabled: boolean; recommended: boolean }

// The five workflows in a fixed order; unavailable ones stay, dim, with the reason.
export function flowRows(v: View): FlowRow[] {
  const ready = v.ready.length
  const decisions = v.openDecisions.length
  const why: Record<Flow, [boolean, string]> = {
    planner: [true, decisions ? `⚑ ${decisions} open` : ''],
    'to-spec': [true, ''],
    'to-tickets': [v.hasSpec, v.hasSpec ? '' : 'needs a specification'],
    impl: [ready + v.mine.length > 0, ready ? `${ready} ready` : v.mine.length ? `${v.mine.length} yours` : 'no ready tickets'],
    orchestrate: [ready >= 2, ready >= 2 ? `${ready} ready` : 'needs 2+ ready tickets'],
  }
  return FLOWS.map(flow => {
    const recommended = flow === v.recommend.flow
    const [enabled, meta] = why[flow]
    return { flow, describe: DESCRIBE[flow], meta: recommended ? '★ recommended' : meta, enabled, recommended }
  })
}

export type Target = { glyph: string; tone: PaneTone; label: string; meta: string; arg: string | null; enabled: boolean }
export type TargetStep = { question: string; items: Target[] }

// The second step of Launch: what the chosen workflow runs on. Choosable rows come first.
export function targetStep(v: View, flow: Flow): TargetStep {
  const whole = (label: string): Target => ({ glyph: '·', tone: 'dim', label, meta: '', arg: null, enabled: true })
  const scopes = (question: string): TargetStep => ({
    question,
    items: [
      ...v.milestones.map((m): Target => ({ glyph: '◆', tone: 'dim', label: m.title, meta: `${m.closed} of ${m.total} done`, arg: m.title, enabled: true })),
      whole('the whole project'),
    ],
  })
  switch (flow) {
    case 'planner': return {
      question: 'SETTLE WHICH DECISION?',
      items: [...v.openDecisions.map((i): Target => ({ glyph: '⚑', tone: 'warning', label: i.title, meta: '', arg: i.title, enabled: true })),
        whole('the whole planning map')],
    }
    case 'to-spec': return { question: 'WRITE THE SHARED SPECIFICATION', items: [] }
    case 'to-tickets': return scopes('TICKETS FOR WHICH SCOPE?')
    case 'orchestrate': return scopes('ORCHESTRATE WHICH SCOPE?')
    case 'impl': {
      const mine = new Set(v.mine.map(i => i.id))
      const blocked = v.blocked.filter(b => !mine.has(b.issue.id))
      const isBlocked = new Set(blocked.map(b => b.issue.id))
      const left = (id: string) => v.ownClaims.find(c => c.issueId === id)?.remainingMs ?? 0
      return {
        question: 'IMPLEMENT WHICH TICKET?',
        items: [
          ...v.mine.map((i): Target => ({ glyph: '●', tone: 'success', label: i.title, meta: `yours · ${duration(left(i.id))}`, arg: i.title, enabled: true })),
          ...v.ready.map((i): Target => ({ glyph: '●', tone: 'success', label: i.title, meta: '', arg: i.title, enabled: true })),
          ...blocked.map((b): Target => ({ glyph: '◌', tone: 'faint', label: b.issue.title, meta: `blocked by ${b.by.join(', ')}`, arg: null, enabled: false })),
          ...v.othersClaims.filter(c => !isBlocked.has(c.issueId))
            .map((c): Target => ({ glyph: '◐', tone: 'faint', label: c.title, meta: `${c.owner} · ${duration(c.remainingMs)}`, arg: null, enabled: false })),
        ],
      }
    }
  }
}

// The command a target step puts in the prompt; null while the chosen row is not choosable.
export function stepCommand(v: View, flow: Flow, sel: number): string | null {
  const { items } = targetStep(v, flow)
  if (items.length === 0) return launchText(flow, v.project!.title, null)
  const item = items[sel]
  return item?.enabled ? launchText(flow, v.project!.title, item.arg) : null
}

export function launchHints(v: View, ui: PaneUi): string {
  if (!ui.step) return hints(choose(FLOWS.length), 'Esc close')
  const command = stepCommand(v, ui.step, ui.sel) !== null
  return hints(command && 'Enter put in prompt', command && runsNow(ui.step) && 'r run now', choose(targetStep(v, ui.step).items.length), 'Esc back')
}
