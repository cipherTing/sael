import { expect, it } from 'vitest'
import { buildRequestSeries, buildTrafficBuckets } from './overviewData'

it('places sparse minute counts into the full selected time window', () => {
  const result = buildTrafficBuckets('2026-09-24T00:00:00Z', 1, [
    { time: '2026-09-24T00:02:00Z', outcome: 'disabled', count: 1 },
    { time: '2026-09-24T00:19:00Z', outcome: 'blocked', count: 2 },
    { time: '2026-09-24T00:20:00Z', outcome: 'clean', count: 3 }
  ])
  expect(result.labels).toHaveLength(12)
  expect(result.counts.disabled).toEqual([1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0])
  expect(result.counts.blocked).toEqual([0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0])
  expect(result.counts.clean).toEqual([0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0])
})

it('keeps invalid requests visible in the request outcome distribution', () => {
  const result = buildTrafficBuckets('2026-09-24T00:00:00Z', 24, [
    { time: '2026-09-24T01:00:00Z', outcome: 'invalid_json', count: 2 },
    { time: '2026-09-24T02:00:00Z', outcome: 'unreviewed', count: 1, upstream_errors: 1 }
  ])
  expect(result.labels).toHaveLength(24)
  expect(result.counts.invalid_json[1]).toBe(2)
  expect(result.counts.unreviewed[2]).toBe(1)
  expect(result.upstreamErrors[2]).toBe(1)
})

it('includes request read and gateway failures in the traffic denominator', () => {
  const result = buildTrafficBuckets('2026-09-24T00:00:00Z', 1, [
    { time: '2026-09-24T00:01:00Z', outcome: 'request_error', count: 2 },
    { time: '2026-09-24T00:02:00Z', outcome: 'gateway_error', count: 1 }
  ])
  expect(result.counts.request_error[0]).toBe(2)
  expect(result.counts.gateway_error[0]).toBe(1)
  expect(buildRequestSeries(result).total[0]).toBe(3)
})

it('keeps the partial first minute that the overview API includes', () => {
  const result = buildTrafficBuckets('2026-09-24T00:00:30Z', 1, [
    { time: '2026-09-24T00:00:00Z', outcome: 'clean', count: 1 }
  ])
  expect(result.counts.clean[0]).toBe(1)
})

it('builds a total traffic series without losing sparse outcomes', () => {
  const buckets = buildTrafficBuckets('2026-09-24T00:00:00Z', 1, [
    { time: '2026-09-24T00:01:00Z', outcome: 'disabled', count: 2 },
    { time: '2026-09-24T00:02:00Z', outcome: 'blocked', count: 1 },
    { time: '2026-09-24T00:06:00Z', outcome: 'invalid_json', count: 3 },
    { time: '2026-09-24T00:07:00Z', outcome: 'unreviewed', count: 1 }
  ])

  expect(buildRequestSeries(buckets)).toEqual({
    total: [3, 4, ...Array(10).fill(0)],
    blocked: [1, 0, ...Array(10).fill(0)],
    unreviewed: [0, 1, ...Array(10).fill(0)]
  })
})

it('aligns hourly buckets to the clock and includes both partial edge hours', () => {
  const result = buildTrafficBuckets('2026-09-23T10:37:30Z', 24, [
    { time: '2026-09-23T10:37:00Z', outcome: 'blocked', count: 1 },
    { time: '2026-09-24T10:37:00Z', outcome: 'disabled', count: 2 }
  ])

  expect(result.labels).toHaveLength(25)
  expect(result.labels[0]).toBe(new Date('2026-09-23T10:00:00Z').toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }))
  expect(result.labels[1]).toBe(new Date('2026-09-23T11:00:00Z').toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }))
  expect(result.ranges[0]).toEqual({ start: Date.parse('2026-09-23T10:37:30Z'), end: Date.parse('2026-09-23T11:00:00Z') })
  expect(result.ranges[24]).toEqual({ start: Date.parse('2026-09-24T10:00:00Z'), end: Date.parse('2026-09-24T10:37:30Z') })
  expect(result.counts.blocked[0]).toBe(1)
  expect(result.counts.disabled[24]).toBe(2)
})

it('keeps a complete date and time label for every timeline bucket', () => {
  const since = new Date(2026, 8, 23, 10, 37, 30).toISOString()
  const result = buildTrafficBuckets(since, 24, [])

  expect(result.axisLabels).toHaveLength(25)
  expect(result.axisLabels[0]).toBe('2026/09/23 10:00')
  expect(result.axisLabels[1]).toBe('2026/09/23 11:00')
  expect(result.axisLabels[24]).toBe('2026/09/24 10:00')
})
