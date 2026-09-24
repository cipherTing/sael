import { afterEach, expect, it, vi } from 'vitest'
import { request } from './api'

afterEach(() => vi.unstubAllGlobals())

it('turns a stale policy response into a useful error', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('policy changed', { status: 409 })))
  await expect(request('/admin/policy', { method: 'PUT', body: '{}' })).rejects.toThrow(/策略已被其他管理员修改/)
})

it('sends same-origin session credentials', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"ok":true}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetch)
  await request('/admin/session')
  expect(fetch).toHaveBeenCalledWith('/admin/session', expect.objectContaining({ credentials: 'same-origin' }))
})
