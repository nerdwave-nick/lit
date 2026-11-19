// Where the mod finds its settings and lit's files. Pure: the reads happen in register.tsx.
import type { Config } from '../types'

// Setup writes `config.json` beside the mod's .claude-plugin directory:
//   {"schema_version": 1, "status": "mod" | "both", "lit": "/absolute/path/to/lit"}
// Without it (a development checkout loaded with --plugin-dir) the mod runs `lit` from PATH
// in mode `both`.
export const CONFIG_FILE = 'config.json'
const DEFAULT: Config = { mode: 'both', lit: 'lit', source: 'default', problem: null }

export function parseConfig(text: string | null): Config {
  if (text === null) return DEFAULT
  const fallback = (problem: string): Config => ({ ...DEFAULT, problem: `${CONFIG_FILE} ${problem}` })
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    return fallback('is not JSON')
  }
  if (typeof data !== 'object' || data === null || Array.isArray(data)) return fallback('is not an object')
  const o = data as Record<string, unknown>
  if (o.schema_version !== 1) return fallback('schema_version must be 1')
  if (o.status !== 'mod' && o.status !== 'both') return fallback('status must be "mod" or "both"')
  if (typeof o.lit !== 'string' || o.lit === '') return fallback('lit must be a non-empty path')
  return { mode: o.status, lit: o.lit, source: 'file', problem: null }
}

export type StateEnv = { override: string | undefined; xdg: string | undefined; home: string | undefined; localAppData: string | undefined }

// lit's workflow state directory, resolved as lit resolves it: LIT_WORKFLOW_STATE_DIR (absolute
// or ~), else the platform default. `os` is `uname -s`, null where uname is missing (Windows).
// Null when nothing locates it; a relative override is relative to lit's own directory.
export function workflowStateDir(env: StateEnv, os: string | null): string | null {
  const { override, home } = env
  if (override) {
    if (override === '~' || override.startsWith('~/')) return home ? home + override.slice(1) : null
    return override.startsWith('/') || /^[A-Za-z]:\\/.test(override) ? override : null
  }
  if (os === null) return env.localAppData ? `${env.localAppData}\\lit\\local` : null
  if (!home) return null
  if (os === 'Darwin') return `${home}/Library/Application Support/lit/local`
  const xdg = env.xdg?.startsWith('/') ? env.xdg : `${home}/.local/state`
  return `${xdg}/lit/local`
}

// Whether an agent's Bash command runs lit (by name or path), which makes the mod refresh.
export function invokesLit(command: string): boolean {
  return /(^|[\s;&|(`])([^\s;&|(`]*\/)?lit(?=$|[\s;&|)`])/.test(command)
}
