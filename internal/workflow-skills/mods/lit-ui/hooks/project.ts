// What the pane's Project view shows: the projects to choose from, and the new project's title
// checked as the tracker would check it. Pure (no `$`), so it is unit-tested; project-view.tsx
// draws it, project-acts.ts acts on it.
import type { Project, View } from '../types'

export type ProjectRow = { project: Project; group: 'current' | 'repo' | 'other' }

// The https form a project records for a repository, from a git remote: scp-like
// (`git@host:o/r.git`), ssh, git, http and https URLs lose the user, the ssh port, `.git` and
// trailing slashes. Local paths and anything else without a host and a path have none.
export function canonicalRepo(remote: string): string | null {
  const url = remote.trim()
  let host: string, port = '', path: string
  const scp = /^(?:[^@/\s]+@)?([^:/\s]{2,}):(?!\/\/)(.*)$/.exec(url)
  const full = /^(https?|ssh|git|git\+ssh|ssh\+git):\/\/(?:[^@/]*@)?([^/:\s]+)(?::(\d+))?(\/.*)?$/i.exec(url)
  if (full) {
    [host, path] = [full[2]!, full[4] ?? '']
    if (/^https?$/i.test(full[1]!) && full[3]) port = `:${full[3]}`
  } else if (scp) {
    [host, path] = [scp[1]!, scp[2]!]
  } else return null
  path = path.replace(/^\/+/, '').replace(/\/+$/, '').replace(/\.git$/, '').replace(/\/+$/, '')
  if (!path || /[\\\s]/.test(path) || /[\\\s]/.test(host)) return null
  return `https://${host.toLowerCase()}${port}/${path}`
}

// The form two repository refs are compared in: hosts and paths ignore case.
const sameRepo = (ref: string) => (canonicalRepo(ref) ?? ref.trim()).toLowerCase()

const MAX_TITLE = 128
const SEGMENT = /^[A-Za-z0-9][A-Za-z0-9_-]*$/

// Why lit would refuse `title` for a new project, or null when it would take it: the tracker's
// prefixes (none known: not checked here), its segment grammar, 128 characters and no existing
// title in any case.
export function validateTitle(title: string, prefixes: string[], projects: Project[]): string | null {
  if (!title) return 'Enter a title such as feat/my-effort'
  if (title.length > MAX_TITLE) return `At most ${MAX_TITLE} characters`
  const [prefix, ...rest] = title.split('/')
  if (prefixes.length > 0 && !prefixes.includes(prefix!)) return `Start with a prefix: ${prefixes.join(', ')}`
  if (rest.length === 0 || ![prefix!, ...rest].every(s => SEGMENT.test(s)))
    return 'After the prefix: letters, digits, - and _, each part starting with a letter or digit'
  const key = title.toLowerCase()
  if (projects.some(p => p.title.toLowerCase() === key)) return 'A project with this title exists'
  return null
}

// CHOOSE A PROJECT: the current project, then this repository's (its git origin among their
// repository refs), each by title; the others only when all are asked for, else counted for more….
export function projectChoices(v: View, all: boolean): { rows: ProjectRow[]; more: number } {
  const repo = v.origin ? canonicalRepo(v.origin)?.toLowerCase() ?? null : null
  const current = v.project?.id ?? null
  const group = (p: Project): ProjectRow['group'] =>
    p.id === current ? 'current' : repo && p.repository_refs.some(r => sameRepo(r) === repo) ? 'repo' : 'other'
  const order = { current: 0, repo: 1, other: 2 }
  const rows = v.projects.map(project => ({ project, group: group(project) }))
    .sort((a, b) => order[a.group] - order[b.group] || a.project.title.localeCompare(b.project.title))
  const others = rows.filter(r => r.group === 'other').length
  return all ? { rows, more: 0 } : { rows: rows.filter(r => r.group !== 'other'), more: others }
}
