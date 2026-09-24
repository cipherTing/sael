import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import EventsPage from './EventsPage'
import type { Event } from './types'

const event: Event = { id: 'event-1', time: '2026-09-23T00:00:00Z', kind: 'hit', request_id: 'request-1', protocol: 'openai_chat', endpoint: '/v1/chat/completions', model: 'test', stream: false, has_non_text_input: false, text_preview: '', policy_version: 2, classifier_ms: 12, decision: { action: 'block', hits: [{ question: 'cyber_abuse', value: 0.9, threshold: 0.8 }], scene_id: 'scene', scene_name: '测试场景', scene_priority: 1 } }
afterEach(cleanup)

it('opens a hit record with the effective scene and historical threshold', () => {
  render(<EventsPage events={[event]} loading={false} error="" onFilter={() => {}} onMore={() => {}} />)
  fireEvent.click(screen.getByText('request-1'))
  expect(screen.getAllByText(/测试场景/).length).toBeGreaterThan(0)
  expect(screen.getByText(/0.9 > 0.8/)).toBeTruthy()
})

it('uses a compact empty state', () => {
  render(<EventsPage events={[]} loading={false} error="" onFilter={() => {}} onMore={() => {}} />)
  expect(screen.getByText('暂无数据')).toBeTruthy()
})

it('shows a compact result summary before the event table', () => {
  render(<EventsPage events={[event]} loading={false} error="" onFilter={() => {}} onMore={() => {}} />)
  expect(screen.getByText(/当前 1 条/)).toBeTruthy()
  expect(screen.getByText(/已拦截 1/)).toBeTruthy()
})
