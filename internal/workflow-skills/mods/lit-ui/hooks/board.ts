// What the pane's Board view shows: the work tickets by state, the longest chain of open
// blockers and the milestones' progress. Pure (no `$`), so it is unit-tested; board-view.tsx
// draws it.
import { duration, isWorkKind } from './model'
import type { PaneTone } from './pane'
import type { Issue, View } from '../types'

export type BoardRow = { glyph: string; tone: PaneTone; title: string; meta: string }
export type MilestoneBar = { id: string; title: string; done: string; todo: string; meta: string }
export type BoardModel = { summary: string; rows: BoardRow[]; chain: string[]; milestones: MilestoneBar[] }

const DONE_SHOWN = 3
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

// Each open work ticket is in one row: mine, else blocked, else another's claim, else ready
// (the order Launch's impl step uses). The last few done close the list.
export function board(v: View, barWidth = 24): BoardModel {
  const mine = new Set(v.mine.map(i => i.id))
  const blocked = v.blocked.filter(b => !mine.has(b.issue.id))
  const isBlocked = new Set(blocked.map(b => b.issue.id))
  const others = v.othersClaims.filter(c => !isBlocked.has(c.issueId))
  const done = v.issues.filter(i => i.state === 'closed' && isWorkKind(i))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at) || a.id.localeCompare(b.id))
  const left = (id: string) => {
    const c = v.ownClaims.find(o => o.issueId === id)
    return c ? `you · ${duration(c.remainingMs)}` : 'you'
  }
  const rows: BoardRow[] = [
    ...v.mine.map((i): BoardRow => ({ glyph: '◷', tone: 'accent', title: i.title, meta: left(i.id) })),
    ...v.ready.map((i): BoardRow => ({ glyph: '●', tone: 'success', title: i.title, meta: '' })),
    ...blocked.map((b): BoardRow => ({ glyph: '◌', tone: 'dim', title: b.issue.title, meta: `← ${b.by.join(', ')}` })),
    ...others.map((c): BoardRow => ({ glyph: '◐', tone: 'warning', title: c.title, meta: `${c.owner} · ${duration(c.remainingMs)}` })),
    ...done.slice(0, DONE_SHOWN).map((i): BoardRow => ({ glyph: '✓', tone: 'dim', title: i.title, meta: '' })),
  ]
  const counts: [number, string][] = [[v.mine.length, 'in progress'], [v.ready.length, 'ready'], [blocked.length, 'blocked'],
    [others.length, 'claimed by others'], [done.length, 'done']]
  const parts = counts.filter(([n]) => n > 0).map(([n, what]) => `${n} ${what}`)
  const otherOpen = v.issues.filter(i => i.state === 'open').length - v.work.length
  const summary = parts.length > 0 ? parts.join(' · ')
    : ['No implementation tickets yet', otherOpen > 0 && plural(otherOpen, 'other open issue', 'other open issues')].filter(Boolean).join(' · ')
  const chain = longestChain(v.issues)
  return {
    summary,
    rows,
    chain: chain.length > 1 ? chain : [],
    milestones: v.milestones.map(m => {
      const [filled, todo] = bar(m.closed, m.total, barWidth)
      return { id: m.id, title: m.title, done: filled, todo, meta: `${m.closed} of ${m.total} done` }
    }),
  }
}

// The longest chain of open issues each blocking the next, root first, as titles. Blockers
// closed or outside the project end a chain; a cycle is cut where it closes. Chains computed
// without meeting a cycle are remembered, so an acyclic graph is walked once.
export function longestChain(issues: Issue[]): string[] {
  const byId = new Map(issues.map(i => [i.id, i]))
  const memo = new Map<string, Issue[]>()
  const onPath = new Set<string>()
  const chainTo = (i: Issue): { chain: Issue[]; cut: boolean } => {
    const known = memo.get(i.id)
    if (known) return { chain: known, cut: false }
    onPath.add(i.id)
    let best: Issue[] = []
    let cut = false
    for (const id of i.blocked_by) {
      const b = byId.get(id)
      if (!b || b.state !== 'open') continue
      if (onPath.has(id)) {
        cut = true
        continue
      }
      const c = chainTo(b)
      cut ||= c.cut
      if (c.chain.length > best.length) best = c.chain
    }
    onPath.delete(i.id)
    const chain = [...best, i]
    if (!cut) memo.set(i.id, chain)
    return { chain, cut }
  }
  let longest: Issue[] = []
  for (const i of issues) {
    if (i.state !== 'open') continue
    const { chain } = chainTo(i)
    if (chain.length > longest.length) longest = chain
  }
  return longest.map(i => i.title)
}

// A progress bar `width` cells wide: the done part and the rest.
export function bar(closed: number, total: number, width: number): [string, string] {
  const filled = total > 0 ? Math.min(width, Math.round((width * closed) / total)) : 0
  return ['█'.repeat(filled), '░'.repeat(width - filled)]
}
