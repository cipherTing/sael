export type Action = 'allow' | 'block'
export type Match = 'any' | 'all'
export type Question = { key: string; type: 'noul' | 'score'; max: number }
export type Condition = { question: string; threshold: number }
export type Scene = { id: string; name: string; note?: string; conditions: Condition[]; match: Match; action: Action }
export type Policy = {
  enabled: boolean
  version: number
  scenes: Scene[]
  preview_chars: number | null
  retention_days: number | null
}
export type PolicyResponse = Policy & { questions: Question[] }

export function simulate(policy: Policy, scores: Record<string, number>): { name: string; action: Action } {
  for (const scene of policy.scenes) {
    const matched = scene.match === 'all'
      ? scene.conditions.every(condition => scores[condition.question] > condition.threshold)
      : scene.conditions.some(condition => scores[condition.question] > condition.threshold)
    if (matched) return { name: scene.name, action: scene.action }
  }
  return { name: '', action: 'allow' }
}
