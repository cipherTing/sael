import type { Action } from './policy'

export type Hit = { question: string; value: number; threshold: number }
export type Answer = { question: string; type: string; value: number }
export type Event = {
  id: string; time: string; kind: 'hit' | 'failure'; request_id: string; protocol: string;
  endpoint: string; model: string; stream: boolean; has_non_text_input: boolean;
  text_preview: string; scores?: Answer[]; policy_version: number; classifier_ms: number;
  error_kind?: string;
  decision: { action: Action; hits: Hit[]; scene_id?: string; scene_name?: string; scene_priority?: number; also_matched?: string[] }
}
export type Overview = {
  since: string; updated_at: string; total: number; checked: number; hits: number;
  blocked: number; unreviewed: number; no_text: number; disabled: number;
  classifier_avg_ms?: number;
  trend: { time: string; outcome: string; count: number; classifier_sum_ms?: number; classifier_samples?: number }[];
  scenes: { name: string; count: number }[];
  questions: { name: string; count: number }[]
}
export type PolicyChange = { time: string; version: number; actor: string; before: unknown; after: unknown }
