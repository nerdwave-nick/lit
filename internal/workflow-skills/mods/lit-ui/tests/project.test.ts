import { describe, expect, test } from 'claude-code/testing'

import { derive } from '../hooks/model'
import { canonicalRepo, projectChoices, validateTitle } from '../hooks/project'
import { NOW, seed } from './fixtures'
import type { Project, Raw } from '../types'

const project = (id: string, title: string, refs: string[] = []): Project => ({ id, title, description: '', repository_refs: refs })
const PROJECTS = [
  project('p1', 'feat/demo'),
  project('p2', 'feat/zeta', ['https://github.com/o/r']),
  project('p3', 'fix/alpha', ['https://example.org/x', 'https://GitHub.com/O/R']),
  project('p4', 'docs/other', ['https://github.com/x/y']),
  project('p5', 'feat/b'),
]
const view = (overrides: Partial<Raw>) => derive(seed({ projects: PROJECTS, origin: 'git@github.com:o/r.git', ...overrides }), NOW)
const titles = (c: ReturnType<typeof projectChoices>) => c.rows.map(r => `${r.project.title} ${r.group}`)

describe('the canonical https repository ref of a git origin', () => {
  test('scp-like, ssh, git and http remotes become https without .git, user or port of ssh', async () => {
    expect([
      'git@github.com:o/r.git',
      'git@GitHub.com:o/r',
      'ssh://git@github.com:22/o/r.git',
      'git://github.com/o/r.git',
      'https://github.com/o/r',
      'https://token:x-oauth@github.com/o/r.git/',
      'http://github.com/o/r/',
      '  git@github.com:o/r.git\n',
    ].map(canonicalRepo)).toEqual(Array(8).fill('https://github.com/o/r'))
    expect(canonicalRepo('https://git.example.org:8443/team/api.git')).toBe('https://git.example.org:8443/team/api')
    expect(canonicalRepo('git@github.com:O/R.git')).toBe('https://github.com/O/R')
  })

  test('local paths and anything without a host and a path have none', async () => {
    expect(['', '/srv/git/r.git', '../r', 'file:///srv/r.git', 'C:\\repos\\r', 'https://github.com', 'git@github.com:'].map(canonicalRepo))
      .toEqual(Array(7).fill(null))
  })
})

describe('new project title validation', () => {
  const prefixes = ['feat', 'fix', 'docs']
  const check = (t: string) => validateTitle(t, prefixes, PROJECTS)
  test('a valid title has no problem', async () => {
    expect(['feat/new-thing', 'fix/a_b/C9', 'docs/x', `feat/${'a'.repeat(123)}`].map(check)).toEqual([null, null, null, null])
  })
  test('each rule, with its message', async () => {
    expect(check('')).toBe('Enter a title such as feat/my-effort')
    expect(check(`feat/${'a'.repeat(124)}`)).toBe('At most 128 characters')
    expect(check('nope/x')).toBe('Start with a prefix: feat, fix, docs')
    expect(check('FEAT/x')).toBe('Start with a prefix: feat, fix, docs')
    for (const t of ['feat', 'feat/', 'feat/-x', 'feat/a b', 'feat//x', 'feat/x/', 'feat/ü'])
      expect([t, check(t)]).toEqual([t, 'After the prefix: letters, digits, - and _, each part starting with a letter or digit'])
    expect(check('feat/Demo')).toBe('A project with this title exists')
  })
  test('the prefixes come from the tracker; without them any prefix passes here', async () => {
    expect(validateTitle('spike/x', ['spike'], [])).toBeNull()
    expect(validateTitle('anything/x', [], [])).toBeNull()
  })
})

describe('CHOOSE A PROJECT', () => {
  test('the current project, then this repository\'s by title, then more… for the others', async () => {
    const c = projectChoices(view({}), false)
    expect(titles(c)).toEqual(['feat/demo current', 'feat/zeta repo', 'fix/alpha repo'])
    expect(c.more).toBe(2)
    const all = projectChoices(view({}), true)
    expect(titles(all)).toEqual(['feat/demo current', 'feat/zeta repo', 'fix/alpha repo', 'docs/other other', 'feat/b other'])
    expect(all.more).toBe(0)
  })

  test('the current project is listed once even when it belongs to this repository', async () => {
    const c = projectChoices(view({ projectId: 'p2' }), false)
    expect(titles(c)).toEqual(['feat/zeta current', 'fix/alpha repo'])
  })

  test('without a project selected or a usable origin', async () => {
    expect(titles(projectChoices(view({ projectId: null }), false))).toEqual(['feat/zeta repo', 'fix/alpha repo'])
    const none = projectChoices(view({ projectId: null, origin: null }), false)
    expect([none.rows, none.more]).toEqual([[], 5])
    expect(projectChoices(view({ origin: '/srv/git/r.git' }), false).more).toBe(4)
  })
})
