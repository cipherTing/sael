import { expect, it } from 'vitest'
import { simulate, type Policy } from './policy'

const policy: Policy = {
  enabled: true, version: 2, preview_chars: 300, retention_days: 30,
  scenes: [
    { id: 'exception', name: '组合放行', conditions: [{ question: 'gore', threshold: 1.5 }, { question: 'self_harm', threshold: 0.8 }], match: 'all', action: 'allow' },
    { id: 'ordinary', name: '血腥拦截', conditions: [{ question: 'gore', threshold: 1 }], match: 'any', action: 'block' }
  ]
}

it('uses each scene threshold and the first matching priority', () => {
  expect(simulate(policy, { gore: 1.6, self_harm: 0.81 })).toEqual({ name: '组合放行', action: 'allow' })
  expect(simulate(policy, { gore: 1.2, self_harm: 0.81 })).toEqual({ name: '血腥拦截', action: 'block' })
})

it('allows scores outside every scene without a record', () => {
  expect(simulate(policy, { gore: 0.9, self_harm: 0.9 })).toEqual({ name: '', action: 'allow' })
})
