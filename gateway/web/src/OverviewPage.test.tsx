import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { BrowserRouter } from 'react-router'
import { afterEach, expect, it, vi } from 'vitest'
import OverviewPage from './OverviewPage'

vi.mock('react-chartjs-2', () => ({
  Line: () => <div data-testid="line-chart" />,
  Bar: () => <div data-testid="bar-chart" />,
  Doughnut: () => <div data-testid="doughnut-chart" />
}))

afterEach(cleanup)

it('separates historical review coverage from the current review switch', () => {
  render(<BrowserRouter><OverviewPage enabled={false} hours={1} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-23T01:00:00Z', total: 10,
    checked: 6, hits: 2, blocked: 1, unreviewed: 3, no_text: 1, disabled: 0,
    trend: [], scenes: [], questions: []
  }} /></BrowserRouter>)
  expect(screen.getByText('60%')).toBeTruthy()
  expect(screen.getByText('审查失败后转发 3 条')).toBeTruthy()
})

it('shows four decision metrics with counts and explicit rate denominators', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '', total: 1000,
    checked: 900, hits: 90, blocked: 20, unreviewed: 0, no_text: 0, disabled: 100,
    trend: [], scenes: [], questions: []
  }} /></BrowserRouter>)

  const cards = screen.getAllByTestId('overview-kpi')
  expect(cards).toHaveLength(4)
  expect(cards[0].textContent).toContain('请求量')
  expect(cards[0].textContent).toContain('1,000')
  expect(cards[1].textContent).toContain('实际审查')
  expect(cards[1].textContent).toContain('90%')
  expect(cards[1].textContent).toContain('900 / 1,000')
  expect(cards[2].textContent).toContain('阈值命中')
  expect(cards[2].textContent).toContain('90')
  expect(cards[2].textContent).toContain('10%')
  expect(cards[3].textContent).toContain('策略拦截')
  expect(cards[3].textContent).toContain('20')
  expect(cards[3].textContent).toContain('2.22%')
  expect(screen.queryByText('上游故障')).toBeNull()
  expect(screen.queryByText('未审放行')).toBeNull()
})

it('shows no invented percentage without a denominator and no permanent failure cards', () => {
  render(<BrowserRouter><OverviewPage enabled={false} hours={1} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '', total: 0,
    checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [], scenes: [], questions: []
  }} /></BrowserRouter>)

  const cards = screen.getAllByTestId('overview-kpi')
  expect(cards[1].textContent).toContain('—')
  expect(cards[2].textContent).toContain('—')
  expect(cards[3].textContent).toContain('—')
  expect(screen.queryByText(/审查失败后转发/)).toBeNull()
})

it('does not surface upstream response failures in the overview', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '', total: 10,
    checked: 10, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [], scenes: [], questions: []
  }} /></BrowserRouter>)

  expect(screen.queryByText('转发请求返回 5xx：2 条')).toBeNull()
  expect(screen.queryByText(/5xx/)).toBeNull()
})

it('surfaces gateway processing failures only when they occur', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '', total: 2,
    checked: 1, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [{ time: '2026-09-23T00:10:00Z', outcome: 'gateway_error', count: 1 }], scenes: [], questions: []
  }} /></BrowserRouter>)

  expect(screen.getByText('网关处理失败 1 条')).toBeTruthy()
})

it('lets an operator refresh the current time window', () => {
  const refresh = vi.fn()
  render(<BrowserRouter><OverviewPage enabled={true} hours={1} onHoursChange={() => {}} onRefresh={refresh} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '', total: 0, checked: 0,
    hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [], scenes: [], questions: []
  }} /></BrowserRouter>)
  fireEvent.click(screen.getByRole('button', { name: '刷新' }))
  expect(refresh).toHaveBeenCalledTimes(1)
})

it('opens the corresponding record list from the full metric tile', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-24T00:00:00Z', updated_at: '2026-09-24T01:00:00Z', total: 2,
    checked: 2, hits: 1, blocked: 1, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [{ time: '2026-09-24T01:00:00Z', outcome: 'blocked', count: 1 }], scenes: [], questions: []
  }} /></BrowserRouter>)
  expect(screen.getByRole('link', { name: /阈值命中.*1/ }).getAttribute('href')).toBe('/events?kind=hit')
  expect(screen.queryByRole('link', { name: '查看命中' })).toBeNull()
})

it('shows disabled forwarding in the outcome distribution without treating it as a failure', () => {
  render(<BrowserRouter><OverviewPage enabled={false} hours={1} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-23T01:00:00Z', total: 10,
    checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 10,
    trend: [{ time: '2026-09-23T00:00:00Z', outcome: 'disabled', count: 10 }], scenes: [], questions: []
  }} /></BrowserRouter>)
  expect(screen.getByText('透传')).toBeTruthy()
  expect(screen.getAllByTestId('overview-kpi')).toHaveLength(4)
  expect(screen.queryByText('审查关闭期间只有透传请求')).toBeNull()
})

it('shows each outcome share beside the distribution chart', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-24T00:00:00Z', total: 2,
    checked: 1, hits: 1, blocked: 1, unreviewed: 0, no_text: 0, disabled: 1,
    trend: [
      { time: '2026-09-23T01:00:00Z', outcome: 'blocked', count: 1 },
      { time: '2026-09-23T02:00:00Z', outcome: 'disabled', count: 1 }
    ], scenes: [], questions: []
  }} /></BrowserRouter>)

  expect(screen.getAllByText('50%')).toHaveLength(3)
})

it('replaces a one-point trend chart with an actionable sparse-data summary', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-23T01:00:00Z', total: 2,
    checked: 2, hits: 1, blocked: 1, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [{ time: '2026-09-23T01:00:00Z', outcome: 'blocked', count: 1 }], scenes: [], questions: []
  }} /></BrowserRouter>)
  expect(screen.getByRole('heading', { name: '请求量' })).toBeTruthy()
  expect(screen.getByText('1 个活跃时段')).toBeTruthy()
  expect(screen.getByTestId('bar-chart')).toBeTruthy()
  expect(screen.getByRole('link', { name: /阈值命中.*1/ })).toBeTruthy()
})

it('uses a line trend when traffic spans multiple time buckets', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={1} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-23T01:00:00Z', total: 3,
    checked: 3, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0,
    trend: [
      { time: '2026-09-23T00:01:00Z', outcome: 'clean', count: 1 },
      { time: '2026-09-23T00:11:00Z', outcome: 'clean', count: 2 }
    ], scenes: [], questions: []
  }} /></BrowserRouter>)
  expect(screen.getByTestId('line-chart')).toBeTruthy()
  expect(screen.queryByText('1 个活跃时段')).toBeNull()
})

it('prioritizes visual operational panels over descriptive empty-state copy', () => {
  render(<BrowserRouter><OverviewPage enabled={true} hours={24} onHoursChange={() => {}} data={{
    since: '2026-09-23T00:00:00Z', updated_at: '2026-09-23T01:00:00Z', total: 12,
    checked: 10, hits: 5, blocked: 3, unreviewed: 1, no_text: 1, disabled: 0,
    trend: [
      { time: '2026-09-23T00:00:00Z', outcome: 'clean', count: 4 },
      { time: '2026-09-23T00:01:00Z', outcome: 'blocked', count: 3 },
      { time: '2026-09-23T00:01:00Z', outcome: 'hit_allowed', count: 2 },
      { time: '2026-09-23T00:01:00Z', outcome: 'unreviewed', count: 1 }
    ],
    scenes: [{ name: '高风险拦截', count: 3 }],
    questions: [{ name: 'cyber_abuse', count: 4 }, { name: 'violence', count: 2 }]
  }} /></BrowserRouter>)
  expect(screen.getByRole('heading', { name: '请求量' })).toBeTruthy()
  expect(screen.getByText('处理结果分布')).toBeTruthy()
  expect(screen.getByText('风险项排行')).toBeTruthy()
  expect(screen.getByText('场景命中')).toBeTruthy()
})
