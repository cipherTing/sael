import type { Overview } from './types'

type TrendPoint = Overview['trend'][number]

const outcomes = ['clean', 'hit_allowed', 'blocked', 'unreviewed', 'disabled', 'no_text', 'invalid_json', 'request_error', 'gateway_error'] as const

export function buildTrafficBuckets(since: string, hours: number, trend: TrendPoint[]) {
  const stepMinutes = hours === 1 ? 5 : hours === 24 ? 60 : 360
  const size = stepMinutes * 60_000
  const start = Date.parse(since)
  const end = start + hours * 3_600_000
  const alignedStart = Math.floor(start / size) * size
  const length = Math.ceil((end - alignedStart) / size)
  const counts: Record<string, number[]> = Object.fromEntries(outcomes.map(key => [key, Array(length).fill(0)]))
  const latencySum = Array<number>(length).fill(0)
  const latencySamples = Array<number>(length).fill(0)
  const upstreamErrors = Array<number>(length).fill(0)
  const ranges = Array.from({ length }, (_, index) => ({
    start: Math.max(start, alignedStart + size * index),
    end: Math.min(end, alignedStart + size * (index + 1))
  }))
  const labels = Array.from({ length }, (_, index) => {
    const date = new Date(alignedStart + size * index)
    return hours === 168 ? date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit' }) : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  })
  const axisLabels = Array.from({ length }, (_, index) => {
    const date = new Date(alignedStart + size * index)
    const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
    return `${date.getFullYear()}/${String(date.getMonth() + 1).padStart(2, '0')}/${String(date.getDate()).padStart(2, '0')} ${time}`
  })

  for (const point of trend) {
    const time = Date.parse(point.time)
    if (time < Math.floor(start / 60_000) * 60_000 || time >= end) continue
    const index = Math.floor((time - alignedStart) / size)
    if (index < 0 || index >= length) continue
    if (!counts[point.outcome]) counts[point.outcome] = Array(length).fill(0)
    counts[point.outcome][index] += point.count
    latencySum[index] += point.classifier_sum_ms || 0
    latencySamples[index] += point.classifier_samples || 0
    upstreamErrors[index] += point.upstream_errors || 0
  }

  return {
    labels,
    axisLabels,
    ranges,
    counts,
    latency: latencySum.map((sum, index) => latencySamples[index] ? Math.round(sum / latencySamples[index]) : null),
    upstreamErrors,
    granularity: hours === 1 ? '5 分钟' : hours === 24 ? '1 小时' : '6 小时'
  }
}

export type TrafficBuckets = ReturnType<typeof buildTrafficBuckets>

export function buildRequestSeries(buckets: TrafficBuckets) {
  const { counts } = buckets
  return {
    total: buckets.labels.map((_, index) => outcomes.reduce((sum, outcome) => sum + counts[outcome][index], 0)),
    blocked: counts.blocked,
    unreviewed: counts.unreviewed
  }
}
