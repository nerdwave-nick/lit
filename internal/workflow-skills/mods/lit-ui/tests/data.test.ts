import { describe, expect, test } from 'claude-code/testing'

import { errorText, gather, parseIssues } from '../hooks/data'
import type { Io } from '../hooks/data'

const ASSOC = { schema_version: 1, host: 'claude', runtime_id: 'sid-1', local_session: 'agent-1', endpoint: 'http://127.0.0.1:7472',
  client_id: 'me', service_id: 'svc', project_id: 'p1', discussion_id: null, parent_runtime_id: null, status: 'ready' }
const SHOW = 'lit workflow-session --host claude --runtime-id sid-1 show'
const T = 'lit --session agent-1 --endpoint http://127.0.0.1:7472 --format json'
const items = (...xs: unknown[]) => JSON.stringify({ items: xs, next_cursor: null })
const claimJson = { issue_id: 't9', owner_client_id: 'me', owner_actor: { kind: 'agent', name: 'claude' },
  token: 'SECRET-TOKEN', acquired_at: '2026-11-20T00:00:00Z', expires_at: '2026-11-20T00:30:00Z', schema_version: 1 }
const issueJson = (id: string, extra: object = {}) => ({ id, title: `T ${id}`, state: 'open', labels: ['ready-for-agent'], blocked_by: [],
  claim: null, created_at: '2026-11-19T00:00:00Z', body: 'x', ...extra })

// A fake lit and tracker: answers by the command line; records every call.
function fakeIo(answers: Record<string, { exitCode?: number; stdout?: string; stderr?: string }>, meta: { status: number; text: string } | 'down') {
  const calls: string[] = []
  const io: Io = {
    run: async argv => {
      const line = argv.join(' ')
      calls.push(line)
      const a = answers[line]
      if (!a) return { exitCode: 1, stdout: '', stderr: `unexpected: ${line}` }
      return { exitCode: a.exitCode ?? 0, stdout: a.stdout ?? '', stderr: a.stderr ?? '' }
    },
    meta: async url => {
      calls.push(`GET ${url}`)
      if (meta === 'down') throw new Error('connection refused')
      return meta
    },
  }
  return { io, calls }
}
const META_OK = { status: 200, text: JSON.stringify({ data: { title_prefixes: ['feat', 'fix'] } }) }
const ready = {
  [SHOW]: { stdout: JSON.stringify(ASSOC) },
  [`${T} session get`]: { stdout: items({ project_id: 'p1', stage: 'impl', status: 'connected' }) },
  [`${T} projects list --all`]: { stdout: items({ id: 'p1', title: 'feat/demo', description: '# D', repository_refs: null }) },
  [`${T} claims list --all`]: { stdout: items(claimJson) },
  [`${T} issues list --all --project p1`]: { stdout: items(issueJson('t1', { claim: { ...claimJson, issue_id: 't1', owner_client_id: 'other' } })) },
  [`${T} milestones list --all --project p1`]: { stdout: items({ id: 'm1', title: 'M1', issue_ids: ['t1'], progress: { closed: 1, total: 3 } }) },
  [`${T} issues get t9`]: { stdout: items(issueJson('t9', { title: 'Elsewhere' })) },
}
const input = { sid: 'sid-1', lit: 'lit', defaultEndpoint: 'http://127.0.0.1:7411' }

describe('gather', () => {
  test('reads the association with show, then the tracker with its session, endpoint and JSON', async () => {
    const { io, calls } = fakeIo(ready, META_OK)
    const raw = await gather(io, input)
    expect(calls).toEqual([SHOW, 'GET http://127.0.0.1:7472/v1/meta', `${T} session get`, `${T} projects list --all`, `${T} claims list --all`,
      `${T} issues list --all --project p1`, `${T} milestones list --all --project p1`, `${T} issues get t9`])
    expect(calls.some(c => c.includes(' run '))).toBe(false)
    expect(raw).toEqual({
      sid: 'sid-1', endpoint: 'http://127.0.0.1:7472', reachable: true, degraded: false, prefixes: ['feat', 'fix'],
      assoc: { local_session: 'agent-1', endpoint: 'http://127.0.0.1:7472', client_id: 'me', project_id: 'p1', status: 'ready' },
      projectId: 'p1', stage: 'impl', stageKnown: true,
      projects: [{ id: 'p1', title: 'feat/demo', description: '# D', repository_refs: [] }],
      issues: [{ id: 't1', title: 'T t1', state: 'open', labels: ['ready-for-agent'], blocked_by: [], created_at: '2026-11-19T00:00:00Z',
        claim: { issue_id: 't1', owner_client_id: 'other', owner_name: 'claude', expires_at: '2026-11-20T00:30:00Z' } }],
      claims: [{ issue_id: 't9', owner_client_id: 'me', owner_name: 'claude', expires_at: '2026-11-20T00:30:00Z' }],
      otherTitles: { t9: 'Elsewhere' },
      milestones: [{ id: 'm1', title: 'M1', issue_ids: ['t1'], closed: 1, total: 3 }],
      errors: [],
    })
    expect(JSON.stringify(raw)).not.toContain('SECRET-TOKEN')
  })

  test('no association: nosession probes the default endpoint and reads nothing else', async () => {
    const notFound = { exitCode: 3, stdout: '{"error":{"code":"association_not_found","message":"none","details":{}}}' }
    const { io, calls } = fakeIo({ [SHOW]: notFound }, META_OK)
    const raw = await gather(io, input)
    expect([raw.assoc, raw.reachable, raw.endpoint, raw.errors]).toEqual([null, true, 'http://127.0.0.1:7411', []])
    expect(calls).toEqual([SHOW, 'GET http://127.0.0.1:7411/v1/meta'])
  })

  test('a pending association reads nothing from the tracker', async () => {
    const { io, calls } = fakeIo({ [SHOW]: { stdout: JSON.stringify({ ...ASSOC, status: 'pending', client_id: null }) } }, META_OK)
    expect((await gather(io, input)).assoc?.status).toBe('pending')
    expect(calls.length).toBe(2)
  })

  test('an unreadable association is an error, not a session', async () => {
    const { io } = fakeIo({ [SHOW]: { exitCode: 1, stderr: 'association record is damaged\nmore' } }, META_OK)
    const raw = await gather(io, input)
    expect([raw.assoc, raw.errors]).toEqual([null, ['workflow-session show: association record is damaged']])
  })

  test('offline and degraded stop before the tracker reads', async () => {
    const down = fakeIo(ready, 'down')
    expect([(await gather(down.io, input)).reachable, down.calls.length]).toEqual([false, 2])
    const degraded = fakeIo(ready, { status: 503, text: '{"error":{"code":"recovery_required","message":"m","details":{}}}' })
    const raw = await gather(degraded.io, input)
    expect([raw.reachable, raw.degraded, degraded.calls.length]).toEqual([true, true, 2])
  })

  test('a failed read is reported, the association keeps the project and the stage is unknown', async () => {
    const { io } = fakeIo({ ...ready, [`${T} session get`]: { exitCode: 4, stdout: '{"error":{"code":"x","message":"session is busy"}}' } }, META_OK)
    const raw = await gather(io, input)
    expect(raw.projectId).toBe('p1')
    expect([raw.stage, raw.stageKnown]).toEqual([null, false])
    expect(raw.errors).toEqual(['session get: session is busy'])
  })

  test('claims that cannot be read stay unknown', async () => {
    const { io } = fakeIo({ ...ready, [`${T} claims list --all`]: { stdout: 'garbage' } }, META_OK)
    const raw = await gather(io, input)
    expect(raw.claims).toBe(null)
    expect(raw.errors).toEqual(['claims list: unexpected output'])
  })
})

describe('parsing tracker output', () => {
  test('malformed issues are refused', async () => {
    expect(() => parseIssues('{"items":[{"id":1}]}')).toThrow('unexpected output')
    expect(() => parseIssues('{"items":[{"id":"a","title":"A","state":"weird","created_at":"x"}]}')).toThrow('unexpected output')
    expect(() => parseIssues('{}')).toThrow('unexpected output')
    expect(parseIssues('{"items":[{"id":"a","title":"A","state":"open","labels":null,"blocked_by":null,"claim":null,"created_at":"x"}]}'))
      .toEqual([{ id: 'a', title: 'A', state: 'open', labels: [], blocked_by: [], claim: null, created_at: 'x' }])
  })
})

describe('lit errors', () => {
  test('the message of lit\'s JSON error, on stderr as lit writes it, or on stdout', async () => {
    const error = '{"error":{"code":"claim_expired","message":"cannot renew an absent or expired claim","details":{}}}'
    expect(errorText({ exitCode: 3, stdout: '', stderr: error })).toBe('cannot renew an absent or expired claim')
    expect(errorText({ exitCode: 3, stdout: error, stderr: '' })).toBe('cannot renew an absent or expired claim')
    expect(errorText({ exitCode: 1, stdout: '', stderr: 'boom\nmore' })).toBe('boom')
    expect(errorText({ exitCode: 2, stdout: '', stderr: '' })).toBe('exit 2')
  })
})
