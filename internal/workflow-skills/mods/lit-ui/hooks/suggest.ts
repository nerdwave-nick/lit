// The next-stage suggestion: when the session's stage clears after planner, to-spec or
// to-tickets, the following stage's command is proposed once as the prompt's ghost text.
// Pure: the caller keeps the watch between refreshes and offers `pending` until it is shown.
import { launchText } from './model'
import type { Flow, Raw, View } from '../types'

// The stage whose clearing leads to each following one.
const FOLLOWS = new Map<string, Flow>([['planner', 'to-spec'], ['to-spec', 'to-tickets'], ['to-tickets', 'orchestrate']])

// One refresh's reading of the stage. `key` names the lit client: a stage belongs to it.
export type StageSeen = { key: string; projectId: string | null; stage: string | null }
export type StageReading = StageSeen & { project: string | null }
// The last reading, and a suggestion not yet shown.
export type StageWatch = { seen: StageSeen | null; pending: string | null }

// The stage as this refresh read it, or null when it could not be read.
export function stageReading(raw: Raw, view: View): StageReading | null {
  if ((view.state !== 'ready' && view.state !== 'noproject') || !raw.assoc || !raw.stageKnown) return null
  return {
    key: `${raw.assoc.local_session}/${raw.assoc.client_id ?? ''}`,
    projectId: view.project?.id ?? null,
    project: view.project?.title ?? null,
    stage: raw.stage,
  }
}

// Compares this refresh with the last one. A refresh that read nothing changes nothing.
export function watchStage(w: StageWatch, r: StageReading | null): StageWatch {
  if (!r) return w
  const seen: StageSeen = { key: r.key, projectId: r.projectId, stage: r.stage }
  const prev = w.seen
  const same = prev !== null && prev.key === r.key && prev.projectId === r.projectId
  // Only the same client, on the same selected project, still without a stage keeps a suggestion.
  if (!same || r.projectId === null || r.project === null || r.stage !== null) return { seen, pending: null }
  const following = prev.stage === null ? undefined : FOLLOWS.get(prev.stage)
  return { seen, pending: following ? launchText(following, r.project, null) : w.pending }
}

// The suggestion `text` is shown: it is not offered again.
export function suggestionShown(w: StageWatch, text: string): StageWatch {
  return w.pending === text ? { ...w, pending: null } : w
}
