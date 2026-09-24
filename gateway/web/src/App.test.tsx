import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { BrowserRouter } from 'react-router'
import { afterEach, expect, it, vi } from 'vitest'
import App from './App'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.history.pushState({}, '', '/') })

it('logs in and loads the operational overview', async () => {
  const fetch = vi.fn(async (path: string) => {
    if (path === '/admin/session') return new Response('unauthorized', { status: 401 })
    if (path === '/admin/login') return Response.json({ authenticated: true })
    if (path === '/admin/policy') return Response.json({ enabled: false, version: 1, thresholds: {}, scenes: [], unmatched_action: '', preview_chars: null, retention_days: null, questions: [] })
    if (path === '/admin/runtime') return Response.json({ classifier: 'error', timeout_ms: 5000, concurrency: 32, in_flight: 0, last_error_kind: 'classifier_timeout' })
    if (path.startsWith('/admin/overview')) return Response.json({ since: '2026-09-23T00:00:00Z', updated_at: '0001-01-01T00:00:00Z', total: 0, checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0, trend: [], scenes: [], questions: [] })
    throw new Error(`unexpected ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  render(<BrowserRouter><App /></BrowserRouter>)
  await screen.findByLabelText(/管理员密码/)
  fireEvent.change(screen.getByLabelText(/管理员密码/), { target: { value: 'secret' } })
  fireEvent.click(screen.getByRole('button', { name: '登录' }))
  await waitFor(() => expect(screen.getByRole('heading', { name: '请求量' })).toBeTruthy(), { timeout: 10000 })
  expect(screen.getAllByText(/透传中/).length).toBeGreaterThan(0)
  expect(screen.getByText(/分类器异常/)).toBeTruthy()
}, 10000)

it('keeps the event filter when loading the next page', async () => {
  window.history.pushState({}, '', '/events?kind=failure')
  const events = Array.from({ length: 50 }, (_, i) => ({ id: `event-${i}`, time: '2026-09-23T00:00:00Z', kind: 'failure', request_id: `request-${i}`, protocol: 'openai_chat', endpoint: '/v1/chat/completions', model: 'test', stream: false, has_non_text_input: false, text_preview: '', policy_version: 1, classifier_ms: 1, decision: { action: 'allow', hits: [] } }))
  const fetch = vi.fn(async (path: string) => {
    if (path === '/admin/session') return Response.json({ authenticated: true })
    if (path === '/admin/policy') return Response.json({ enabled: false, version: 1, thresholds: {}, scenes: [], unmatched_action: '', preview_chars: null, retention_days: null, questions: [] })
    if (path === '/admin/policy-changes') return Response.json([])
    if (path === '/admin/runtime') return Response.json({ classifier: 'not_checked', timeout_ms: 5000, concurrency: 32 })
    if (path.startsWith('/admin/overview')) return Response.json({ since: '', updated_at: '', total: 0, checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0, trend: [], scenes: [], questions: [] })
    if (path.startsWith('/admin/events')) return Response.json(events)
    throw new Error(`unexpected ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  render(<BrowserRouter><App /></BrowserRouter>)
  fireEvent.click(await screen.findByRole('button', { name: '加载更多' }, { timeout: 10000 }))
  await waitFor(() => expect(fetch.mock.calls.some(([path]) => path.includes('kind=failure') && path.includes('offset=50'))).toBe(true))
}, 20000)

it('opens saved Jev configuration and text debugging from settings', async () => {
  window.history.pushState({}, '', '/settings')
  const fetch = vi.fn(async (path: string) => {
    if (path === '/admin/session') return Response.json({ authenticated: true })
    if (path === '/admin/policy') return Response.json({ enabled: false, version: 1, thresholds: {}, scenes: [], unmatched_action: '', preview_chars: null, retention_days: null, questions: [] })
    if (path === '/admin/jev') return Response.json({ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, updated_at: '' })
    if (path === '/admin/policy-changes') return Response.json([])
    if (path === '/admin/runtime') return Response.json({ classifier: 'not_checked', timeout_ms: 5000, concurrency: 32 })
    if (path.startsWith('/admin/overview')) return Response.json({ since: '', updated_at: '', total: 0, checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0, trend: [], scenes: [], questions: [] })
    throw new Error(`unexpected ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  render(<BrowserRouter><App /></BrowserRouter>)
  fireEvent.click(await screen.findByRole('tab', { name: 'Jev 连接与调试' }))
  expect(await screen.findByDisplayValue('jev-test')).toBeTruthy()
  expect(screen.getByRole('button', { name: '测试 Jev 分类器' })).toBeTruthy()
})

it('keeps the settings tab addressable after refresh', async () => {
  window.history.pushState({}, '', '/settings?tab=1')
  const fetch = vi.fn(async (path: string) => {
    if (path === '/admin/session') return Response.json({ authenticated: true })
    if (path === '/admin/policy') return Response.json({ enabled: false, version: 1, thresholds: {}, scenes: [], unmatched_action: '', preview_chars: null, retention_days: null, questions: [] })
    if (path === '/admin/jev') return Response.json({ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, updated_at: '' })
    if (path === '/admin/policy-changes') return Response.json([])
    if (path === '/admin/runtime') return Response.json({ classifier: 'not_checked', timeout_ms: 5000, concurrency: 32 })
    if (path.startsWith('/admin/overview')) return Response.json({ since: '', updated_at: '', total: 0, checked: 0, hits: 0, blocked: 0, unreviewed: 0, no_text: 0, disabled: 0, trend: [], scenes: [], questions: [] })
    throw new Error(`unexpected ${path}`)
  })
  vi.stubGlobal('fetch', fetch)
  render(<BrowserRouter><App /></BrowserRouter>)
  expect(await screen.findByRole('tab', { name: 'Jev 连接与调试', selected: true })).toBeTruthy()
})
