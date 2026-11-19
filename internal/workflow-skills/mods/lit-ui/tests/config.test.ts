import { describe, expect, test } from 'claude-code/testing'

import { invokesLit, parseConfig, workflowStateDir } from '../hooks/config'

describe('config', () => {
  test('setup\'s config.json gives the status mode and the lit executable', async () => {
    expect(parseConfig('{"schema_version":1,"status":"mod","lit":"/home/u/go/bin/lit"}'))
      .toEqual({ mode: 'mod', lit: '/home/u/go/bin/lit', source: 'file', problem: null })
  })

  test('without one the dev default is lit on PATH and mode both', async () => {
    expect(parseConfig(null)).toEqual({ mode: 'both', lit: 'lit', source: 'default', problem: null })
  })

  test('an unusable file falls back to the default and says why', async () => {
    const cases: [string, string][] = [
      ['not json', 'config.json is not JSON'],
      ['[]', 'config.json is not an object'],
      ['{"schema_version":2,"status":"mod","lit":"/x/lit"}', 'config.json schema_version must be 1'],
      ['{"schema_version":1,"status":"line","lit":"/x/lit"}', 'config.json status must be "mod" or "both"'],
      ['{"schema_version":1,"status":"both","lit":""}', 'config.json lit must be a non-empty path'],
      ['{"schema_version":1,"status":"both"}', 'config.json lit must be a non-empty path'],
    ]
    for (const [text, problem] of cases) {
      expect(parseConfig(text)).toEqual({ mode: 'both', lit: 'lit', source: 'default', problem })
    }
  })
})

describe('workflow state directory', () => {
  const env = { override: undefined, xdg: undefined, home: '/home/u', localAppData: undefined }
  test('follows lit: LIT_WORKFLOW_STATE_DIR, then the platform default', async () => {
    expect(workflowStateDir({ ...env, override: '/srv/wf' }, 'Linux')).toBe('/srv/wf')
    expect(workflowStateDir({ ...env, override: '~/wf' }, 'Linux')).toBe('/home/u/wf')
    expect(workflowStateDir(env, 'Linux')).toBe('/home/u/.local/state/lit/local')
    expect(workflowStateDir({ ...env, xdg: '/xdg' }, 'Linux')).toBe('/xdg/lit/local')
    expect(workflowStateDir({ ...env, xdg: 'relative' }, 'Linux')).toBe('/home/u/.local/state/lit/local')
    expect(workflowStateDir({ ...env, xdg: '/xdg' }, 'Darwin')).toBe('/home/u/Library/Application Support/lit/local')
    expect(workflowStateDir({ ...env, home: undefined, localAppData: 'C:\\Users\\u\\AppData\\Local' }, null))
      .toBe('C:\\Users\\u\\AppData\\Local\\lit\\local')
  })

  test('is unknown when nothing locates it', async () => {
    expect(workflowStateDir({ ...env, home: undefined }, 'Linux')).toBe(null)
    expect(workflowStateDir({ ...env, override: 'relative/wf' }, 'Linux')).toBe(null)
  })
})

describe('lit in a Bash command', () => {
  test('a command that runs lit', async () => {
    for (const c of ['lit claims list', 'cd /x && lit issues get A', 'LIT_SESSION=a lit session get', '/home/u/go/bin/lit --help',
      'x=$(lit workflow-session --host claude --runtime-id r show)', 'git status; lit', 'echo "a" | lit comments create --file -']) {
      expect(invokesLit(c)).toBe(true)
    }
  })

  test('commands that only mention it', async () => {
    for (const c of ['lit-server --help', 'split -l 2 f', 'go test ./internal/litclient', 'cat literal.txt', 'git commit -m "x"']) {
      expect(invokesLit(c)).toBe(false)
    }
  })
})
