// One refresh's reads: the association through `workflow-session show`, reachability from
// /v1/meta, then the tracker through the lit CLI with the association's session and endpoint.
// The engine follows `$` within one file only, so the reads go through an Io built in
// register.tsx. Claim tokens are dropped while parsing and never reach the view.
import type { Assoc, Claim, Issue, Milestone, Project, Raw } from '../types'

export type RunResult = { exitCode: number; stdout: string; stderr: string }
export type Io = {
  // Runs a command outside any checkout; rejects when it cannot start or times out.
  run: (argv: string[]) => Promise<RunResult>
  // GET of the URL; rejects when the tracker does not answer in time.
  meta: (url: string) => Promise<{ status: number; text: string }>
}
export type GatherInput = { sid: string; lit: string; defaultEndpoint: string }

const UNEXPECTED = 'unexpected output'
type Obj = Record<string, unknown>
const isObj = (x: unknown): x is Obj => typeof x === 'object' && x !== null && !Array.isArray(x)
function fail(): never {
  throw new Error(UNEXPECTED)
}
const str = (o: Obj, k: string): string => (typeof o[k] === 'string' ? (o[k] as string) : fail())
const strOrNull = (o: Obj, k: string): string | null => (o[k] === null || o[k] === undefined ? null : str(o, k))
const strs = (o: Obj, k: string): string[] => {
  const v = o[k] ?? []
  return Array.isArray(v) && v.every(s => typeof s === 'string') ? (v as string[]) : fail()
}
function itemsOf(text: string): Obj[] {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    fail()
  }
  if (!isObj(data) || !Array.isArray(data.items) || !data.items.every(isObj)) fail()
  return data.items as Obj[]
}

function parseClaim(o: unknown): Claim {
  if (!isObj(o)) fail()
  const actor = isObj(o.owner_actor) && typeof o.owner_actor.name === 'string' ? o.owner_actor.name : null
  return { issue_id: str(o, 'issue_id'), owner_client_id: str(o, 'owner_client_id'), owner_name: actor, expires_at: str(o, 'expires_at') }
}
export function parseIssues(text: string): Issue[] {
  return itemsOf(text).map(o => {
    const state = o.state === 'open' || o.state === 'closed' ? o.state : fail()
    return {
      id: str(o, 'id'), title: str(o, 'title'), state, labels: strs(o, 'labels'), blocked_by: strs(o, 'blocked_by'),
      claim: o.claim === null || o.claim === undefined ? null : parseClaim(o.claim), created_at: str(o, 'created_at'),
      updated_at: strOrNull(o, 'updated_at') ?? str(o, 'created_at'),
    }
  })
}
const parseClaims = (text: string): Claim[] => itemsOf(text).map(parseClaim)
const parseProjects = (text: string): Project[] => itemsOf(text).map(o =>
  ({ id: str(o, 'id'), title: str(o, 'title'), description: strOrNull(o, 'description') ?? '', repository_refs: strs(o, 'repository_refs') }))
const parseMilestones = (text: string): Milestone[] => itemsOf(text).map(o => {
  const p = isObj(o.progress) ? o.progress : fail()
  const n = (k: string) => (typeof p[k] === 'number' ? (p[k] as number) : fail())
  return { id: str(o, 'id'), title: str(o, 'title'), issue_ids: strs(o, 'issue_ids'), closed: n('closed'), total: n('total') }
})
function parseSession(text: string): { projectId: string | null; stage: string | null; stageKnown: boolean } {
  const s = itemsOf(text)[0] ?? fail()
  return { projectId: strOrNull(s, 'project_id'), stage: strOrNull(s, 'stage'), stageKnown: true }
}
function parseAssoc(text: string): Assoc {
  let o: unknown
  try {
    o = JSON.parse(text)
  } catch {
    fail()
  }
  if (!isObj(o)) fail()
  return { local_session: str(o, 'local_session'), endpoint: str(o, 'endpoint'), client_id: strOrNull(o, 'client_id'),
    project_id: strOrNull(o, 'project_id'), status: str(o, 'status') }
}

// This conversation's association, or null when it has none (`show` exits 3).
export async function readAssoc(io: Io, lit: string, sid: string): Promise<Assoc | null> {
  const r = await io.run([lit, 'workflow-session', '--host', 'claude', '--runtime-id', sid, 'show'])
  if (r.exitCode === 3) return null
  if (r.exitCode !== 0) throw new Error(errorText(r))
  return parseAssoc(r.stdout)
}

// A tracker command line: the association's session and endpoint, JSON output.
export const trackerArgv = (lit: string, a: Assoc, args: string[]) =>
  [lit, '--session', a.local_session, '--endpoint', a.endpoint, '--format', 'json', ...args]

// The message of lit's JSON error (lit writes it to stderr), else the first line it wrote.
export function errorText(r: RunResult): string {
  for (const text of [r.stderr, r.stdout]) {
    try {
      const m = JSON.parse(text)?.error?.message
      if (typeof m === 'string') return m
    } catch {}
  }
  return (r.stderr || r.stdout).trim().split('\n')[0] || `exit ${r.exitCode}`
}

export async function gather(io: Io, input: GatherInput): Promise<Raw> {
  const { sid, lit } = input
  const raw: Raw = {
    sid, endpoint: input.defaultEndpoint, reachable: false, degraded: false, prefixes: [], assoc: null, projectId: null,
    stage: null, stageKnown: false, projects: [], issues: [], claims: null, otherTitles: {}, milestones: [], origin: null, errors: [],
  }
  const note = (what: string, err: unknown) => raw.errors.push(`${what}: ${err instanceof Error ? err.message : String(err)}`)
  try {
    raw.assoc = await readAssoc(io, lit, sid)
  } catch (err) {
    note('workflow-session show', err)
  }
  if (raw.assoc) raw.endpoint = raw.assoc.endpoint
  try {
    const meta = await io.meta(raw.endpoint.replace(/\/+$/, '') + '/v1/meta')
    raw.reachable = true
    raw.degraded = meta.status === 503
    if (meta.status === 200) raw.prefixes = strs(JSON.parse(meta.text).data, 'title_prefixes')
  } catch (err) {
    if (raw.reachable) note('meta', err)
  }
  const assoc = raw.assoc
  if (!raw.reachable || raw.degraded || !assoc || assoc.status !== 'ready') return raw
  // One tracker read; a failure is reported and leaves the fallback.
  const read = async <T>(what: string, args: string[], parse: (text: string) => T, fallback: T): Promise<T> => {
    try {
      const r = await io.run(trackerArgv(lit, assoc, args))
      if (r.exitCode !== 0) throw new Error(errorText(r))
      return parse(r.stdout)
    } catch (err) {
      note(what, err)
      return fallback
    }
  }
  const [session, projects, claims] = await Promise.all([
    read('session get', ['session', 'get'], parseSession, { projectId: assoc.project_id, stage: null, stageKnown: false }),
    read('projects list', ['projects', 'list', '--all'], parseProjects, []),
    read('claims list', ['claims', 'list', '--all'], parseClaims, null),
  ])
  Object.assign(raw, { ...session, projects, claims })
  if (raw.projectId) {
    const project = ['--project', raw.projectId]
    ;[raw.issues, raw.milestones] = await Promise.all([
      read('issues list', ['issues', 'list', '--all', ...project], parseIssues, []),
      read('milestones list', ['milestones', 'list', '--all', ...project], parseMilestones, []),
    ])
  }
  // Titles of this session's claims in other projects.
  const known = new Set(raw.issues.map(i => i.id))
  const foreign = (claims ?? []).map(c => c.issue_id).filter(id => !known.has(id))
  if (foreign.length > 0) {
    for (const i of await read('issues get', ['issues', 'get', ...foreign], parseIssues, [])) raw.otherTitles[i.id] = i.title
  }
  return raw
}
