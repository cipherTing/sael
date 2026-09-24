import { describe, expect, it } from 'vitest'
import { simulate, type Policy } from './policy'

const policy: Policy = {
  enabled: true, version: 2, thresholds: {}, preview_chars: 0, retention_days: 30,
  unmatched_action: 'block', scenes: [
    { id: 'exception', name: '同时命中', questions: ['a','b'], match: 'all', action: 'allow' },
    { id: 'ordinary', name: '任一命中', questions: ['a','b'], match: 'any', action: 'block' }
  ]
}

describe('scene simulation', () => {
  it('uses the first matching scene in priority order', () => {
    expect(simulate(policy, ['a','b'])).toEqual({ name: '同时命中', action: 'allow' })
    expect(simulate(policy, ['a'])).toEqual({ name: '任一命中', action: 'block' })
  })
  it('uses unmatched action when no scene matches', () => {
    expect(simulate(policy, ['c'])).toEqual({ name: '未匹配场景', action: 'block' })
  })
})
