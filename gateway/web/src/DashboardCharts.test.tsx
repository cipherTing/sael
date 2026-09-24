import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { FailureTrendChart, RequestTrendChart, SignalChart, SparseActivityChart } from './DashboardCharts'
import { buildTrafficBuckets } from './overviewData'

const captured = vi.hoisted(() => ({ line: null as any, bar: null as any, doughnut: null as any }))
vi.mock('react-chartjs-2', () => ({
  Line: (props: any) => { captured.line = props; return <div /> },
  Bar: (props: any) => { captured.bar = props; return <div /> },
  Doughnut: (props: any) => { captured.doughnut = props; return <div /> }
}))

afterEach(() => { cleanup(); captured.line = null; captured.bar = null; captured.doughnut = null })

const buckets = buildTrafficBuckets(new Date(2026, 8, 23, 10, 37).toISOString(), 24, [])

it('shows every full date-time tick without spreading buckets too far apart', () => {
  render(<RequestTrendChart buckets={buckets} />)
  expect(captured.line.data.labels).toEqual(buckets.axisLabels)
  const callback = captured.line.options.scales.x.ticks.callback
  const visible = (width: number) => buckets.axisLabels.map((_, index) => callback.call({ width }, index))
  expect(visible(960)).toEqual(buckets.axisLabels)
  expect(visible(320)).toEqual(buckets.axisLabels)
  expect(captured.line.options.scales.x.ticks.autoSkip).toBe(false)
  const viewport = screen.getByTestId('timeline-scroll')
  expect(getComputedStyle(viewport).overflowX).toBe('auto')
  expect(parseInt(getComputedStyle(viewport.firstElementChild as HTMLElement).minWidth)).toBeLessThanOrEqual(700)
  expect(captured.line.options.scales.x.ticks.minRotation).toBeGreaterThanOrEqual(80)
})

it('uses compact ticks on smaller time-series charts while preserving precise hover ranges', () => {
  render(<SignalChart buckets={buckets} values={buckets.latency} name="分类器延迟" color="#4f78d1" />)
  expect(captured.line.data.labels).toEqual(buckets.labels)
  expect(captured.line.options.plugins.tooltip.callbacks.title([{ dataIndex: 0 }])).toContain('10:37')

  render(<FailureTrendChart buckets={buckets} />)
  expect(captured.line.data.labels).toEqual(buckets.labels)

  render(<SparseActivityChart buckets={buckets} />)
  expect(captured.bar.data.labels).toEqual(buckets.axisLabels)
  expect(buckets.axisLabels.map((_, index) => captured.bar.options.scales.x.ticks.callback(index))).toEqual(buckets.axisLabels)
  expect(parseInt(getComputedStyle(screen.getByTestId('timeline-scroll').firstElementChild as HTMLElement).minWidth)).toBeLessThanOrEqual(700)
  expect(captured.bar.options.scales.x.ticks.minRotation).toBeGreaterThanOrEqual(80)
})

it('shows zero-request intervals in hover details', () => {
  render(<RequestTrendChart buckets={buckets} />)
  const lineFilter = captured.line.options.plugins.tooltip.filter
  expect(lineFilter({ datasetIndex: 0, parsed: { y: 0 } })).toBe(true)
  expect(lineFilter({ datasetIndex: 1, parsed: { y: 0 } })).toBe(false)

  render(<SparseActivityChart buckets={buckets} />)
  const barFilter = captured.bar.options.plugins.tooltip.filter
  expect(barFilter({ dataIndex: 1, parsed: { y: 0 } })).toBe(true)
  expect(captured.bar.options.plugins.tooltip.callbacks.footer([{ dataIndex: 1 }])).toBe('共 0 次')
})

it('keeps a complete ring when one outcome accounts for all requests', async () => {
  const { OutcomeChart } = await import('./DashboardCharts')
  render(<OutcomeChart totals={{ disabled: 10 }} />)
  expect(captured.doughnut.data.datasets[0].spacing).toBe(0)
})
