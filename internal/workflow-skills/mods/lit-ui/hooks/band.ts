// What the band above the prompt shows in each state and status mode. Pure: unit-tested.
import { duration } from './model'
import type { StatusMode, View } from '../types'

export const WARN_MS = 5 * 60000
export type Tone = 'text' | 'dim' | 'warning' | 'error'
export type Fact = { text: string; tone: Tone }
export type BandAction =
  | { kind: 'fill'; command: string }
  | { kind: 'renew'; issueId: string }
  | { kind: 'pane' | 'connect' | 'reconcile' | 'retry' }
export type BandButton = { key: string; label: string; does: BandAction }
export type BandModel = { glyph: string | null; glyphTone: Tone; lead: string; leadBold: boolean; facts: Fact[]; buttons: BandButton[] }

const PANE: BandButton = { key: '2', label: 'lit', does: { kind: 'pane' } }

// A claim's time: "24m", or "3m left" once it is under five minutes.
export const left = (ms: number) => (ms < WARN_MS ? `${duration(ms)} left` : duration(ms))

export function band(v: View, mode: StatusMode): BandModel {
  const quiet = (glyph: string, tone: Tone, lead: string, button: BandButton, facts: Fact[] = []): BandModel =>
    ({ glyph, glyphTone: tone, lead, leadBold: false, facts, buttons: [button] })
  const retry: BandButton = { key: '1', label: 'retry', does: { kind: 'retry' } }
  switch (v.state) {
    case 'offline':
      return quiet('⚫', 'error', 'lit · tracker not answering', retry, [{ text: hostPort(v.endpoint), tone: 'dim' }])
    case 'degraded': return quiet('⚫', 'error', 'lit · tracker needs recovery (docs/recovery.md)', retry)
    case 'nosession': return quiet('○', 'warning', 'lit · not connected in this conversation', { key: '1', label: 'connect', does: { kind: 'connect' } })
    case 'pending': return quiet('○', 'warning', 'lit · connection pending', { key: '1', label: 'reconcile', does: { kind: 'reconcile' } })
    case 'noproject': return quiet('○', 'warning', 'lit · no project selected', { key: '1', label: 'choose project', does: { kind: 'pane' } })
  }
  const both = mode === 'both'
  const claim = v.ownClaims[0]
  const expiring = claim !== undefined && claim.remainingMs < WARN_MS
  const waiting = v.waiting.length
  const facts: Fact[] = []
  if (claim && (!both || expiring)) facts.push({ text: `◷ ${claim.title} ${left(claim.remainingMs)}`, tone: expiring ? 'warning' : 'text' })
  if (waiting > 0) facts.push({ text: `⚑ ${waiting} waiting`, tone: 'warning' })
  if (both && v.recommend.flow) facts.push({ text: `next /${v.recommend.flow}`, tone: 'dim' })
  if (!both && v.stage) facts.push({ text: v.stage, tone: 'dim' })
  const buttons: BandButton[] = []
  if (expiring) buttons.push({ key: '3', label: 'renew', does: { kind: 'renew', issueId: claim.issueId } })
  else if (v.recommend.flow && v.recommend.command) buttons.push({ key: '1', label: v.recommend.flow, does: { kind: 'fill', command: v.recommend.command } })
  buttons.push(PANE)
  return { glyph: both ? null : '🔥', glyphTone: 'text', lead: both ? '' : v.project!.title, leadBold: !both, facts, buttons }
}

// "127.0.0.1:7411" from "http://127.0.0.1:7411/".
export function hostPort(endpoint: string): string {
  return endpoint.replace(/^[a-z]+:\/\//i, '').replace(/\/.*$/, '')
}
