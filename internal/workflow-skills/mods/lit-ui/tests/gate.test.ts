import { describe, expect, test } from 'claude-code/testing'

import { refreshGate } from '../hooks/gate'

describe('refresh gate', () => {
  test('never two refreshes at once; requests during one get one more run, then settle', async () => {
    const gate = refreshGate()
    expect(gate.enter()).toBe(null)
    const settled: string[] = []
    const second = gate.enter()!.then(() => settled.push('second'))
    const third = gate.enter()!.then(() => settled.push('third'))
    expect(gate.again()).toBe(true)
    await Promise.resolve()
    expect(settled).toEqual([])
    expect(gate.again()).toBe(false)
    await Promise.all([second, third])
    expect(settled).toEqual(['second', 'third'])
    expect(gate.enter()).toBe(null)
    expect(gate.again()).toBe(false)
  })
})
