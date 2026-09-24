export type Action = 'allow' | 'block'
export type Match = 'any' | 'all'
export type Question = { key: string; type: 'noul' | 'score'; max: number }
export type Scene = { id: string; name: string; questions: string[]; match: Match; action: Action }
export type Policy = {
  enabled: boolean
  version: number
  thresholds: Record<string, number>
  scenes: Scene[]
  unmatched_action: Action | ''
  preview_chars: number | null
  retention_days: number | null
}
export type PolicyResponse = Policy & { questions: Question[] }

export function simulate(policy: Policy, hits: string[]): { name: string; action: Action } {
  const hitSet = new Set(hits)
  for (const scene of policy.scenes) {
    const matched = scene.match === 'all'
      ? scene.questions.every(key => hitSet.has(key))
      : scene.questions.some(key => hitSet.has(key))
    if (matched) return { name: scene.name, action: scene.action }
  }
  return { name: '未匹配场景', action: policy.unmatched_action || 'allow' }
}
