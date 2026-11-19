// The per-session status feed. Pure: register.tsx writes the file.
import type { View } from '../types'

export type Feed = {
  updatedAt: string
  state: View['state']
  project: string | null
  claims: { title: string; remainingMs: number }[]
  stage: string | null
  ready: number
  waiting: number
  recommend: { flow: View['recommend']['flow']; reason: string }
  errors: string[]
}

// What a user-defined status line reads instead of calling lit.
export function buildFeed(v: View, now: number): Feed {
  return {
    updatedAt: new Date(now).toISOString(),
    state: v.state,
    project: v.project?.title ?? null,
    claims: v.ownClaims.map(c => ({ title: c.title, remainingMs: c.remainingMs })),
    stage: v.stage,
    ready: v.ready.length,
    waiting: v.waiting.length,
    recommend: { flow: v.recommend.flow, reason: v.recommend.reason },
    errors: v.errors,
  }
}

// <workflow state>/ui-feed/<Claude session ID>.json
export function feedPath(stateDir: string, sid: string): string {
  const sep = stateDir.includes('\\') && !stateDir.includes('/') ? '\\' : '/'
  return [stateDir, 'ui-feed', `${sid}.json`].join(sep)
}
