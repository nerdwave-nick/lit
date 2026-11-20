// Claim toasts: one when a claim of this session falls under five minutes, one when it has
// expired. Pure: the caller keeps `watched` between refreshes. Nothing here acts on a claim.
import { WARN_MS } from './band'
import { duration } from './model'
import type { ClaimView } from '../types'

// The own claims seen so far, by issue ID: their expiry and which toasts they have had.
export type Watched = Record<string, { title: string; expiresAt: number; warned: boolean; ended: boolean }>

const expired = (title: string) => `claim on "${title}" has expired`

export function claimNotices(watched: Watched, claims: ClaimView[] | null, now: number): { toasts: string[]; watched: Watched } {
  // Claims that could not be read say nothing about expiry or release.
  if (claims === null) return { toasts: [], watched }
  const toasts: string[] = []
  const next: Watched = {}
  for (const c of claims) {
    const seen = watched[c.issueId]
    // A renewal moves the expiry, so the claim may warn again.
    const same = seen !== undefined && seen.expiresAt === c.expiresAt
    let warned = same && seen.warned
    let ended = same && seen.ended
    const remaining = c.expiresAt - now
    if (remaining <= 0 && !ended) {
      toasts.push(expired(c.title))
      ended = true
    } else if (remaining > 0 && remaining < WARN_MS && !warned) {
      toasts.push(`claim on "${c.title}" expires in ${duration(remaining)}`)
      warned = true
    }
    next[c.issueId] = { title: c.title, expiresAt: c.expiresAt, warned, ended }
  }
  // A claim gone from the list expired when its time is up; before that it was released or closed.
  for (const [id, w] of Object.entries(watched)) {
    if (!(id in next) && now >= w.expiresAt && !w.ended) toasts.push(expired(w.title))
  }
  return { toasts, watched: next }
}
