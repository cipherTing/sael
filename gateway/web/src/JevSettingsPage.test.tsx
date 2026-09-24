import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import JevSettingsPage from './JevSettingsPage'

afterEach(cleanup)

it('requires address, model and key before the first save', async () => {
  const onSave = vi.fn().mockResolvedValue({ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, timeout_ms: 5000, updated_at: '' })
  render(<JevSettingsPage config={{ base_url: '', model: '', api_key_set: false, updated_at: '' }} onSave={onSave} onTest={vi.fn()} />)
  fireEvent.change(screen.getByLabelText('接口地址'), { target: { value: 'https://api.example/v1' } })
  fireEvent.change(screen.getByLabelText('模型 ID'), { target: { value: 'jev-test' } })
  expect(screen.getByRole('button', { name: '保存 Jev 配置' }).hasAttribute('disabled')).toBe(true)
  fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'secret-key' } })
  fireEvent.click(screen.getByRole('button', { name: '保存 Jev 配置' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ base_url: 'https://api.example/v1', model: 'jev-test', api_key: 'secret-key', timeout_ms: 5000 }))
  expect(screen.queryByDisplayValue('secret-key')).toBeNull()
})

it('shows scores and the simulated rule result for a real text test', async () => {
  const onTest = vi.fn().mockResolvedValue({ scores: [{ question: 'cyber_abuse', type: 'noul', value: 0.91 }], decision: { action: 'block', hits: [{ question: 'cyber_abuse', value: 0.91, threshold: 0.7 }], scene_name: '高风险', scene_priority: 1 }, policy_ready: true, classifier_ms: 123 })
  render(<JevSettingsPage config={{ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, updated_at: '' }} onSave={vi.fn()} onTest={onTest} />)
  fireEvent.change(screen.getByLabelText('测试文本'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '测试 Jev 分类器' }))
  await screen.findByText(/高风险.*拦截/)
  expect(screen.getByText(/0.91/)).toBeTruthy()
  expect(onTest).toHaveBeenCalledWith('hello')
})

it('keeps the text test action beside the input and allows clearing the result', async () => {
  const onTest = vi.fn().mockResolvedValue({ scores: [{ question: 'cyber_abuse', type: 'noul', value: 0.91 }], decision: null, policy_ready: false, classifier_ms: 123 })
  render(<JevSettingsPage config={{ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, updated_at: '' }} onSave={vi.fn()} onTest={onTest} />)
  expect(screen.getByRole('button', { name: '测试 Jev 分类器' })).toBeTruthy()
  fireEvent.change(screen.getByLabelText('测试文本'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '测试 Jev 分类器' }))
  await screen.findByText(/审核结果/)
  fireEvent.click(screen.getByRole('button', { name: '清空测试' }))
  expect(screen.queryByText(/审核结果/)).toBeNull()
})

it('does not test an old connection after the saved connection is edited', () => {
  render(<JevSettingsPage config={{ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, updated_at: '' }} onSave={vi.fn()} onTest={vi.fn()} />)
  fireEvent.change(screen.getByLabelText('模型 ID'), { target: { value: 'jev-next' } })
  fireEvent.change(screen.getByLabelText('测试文本'), { target: { value: 'hello' } })
  expect(screen.getByRole('button', { name: '测试 Jev 分类器' }).hasAttribute('disabled')).toBe(true)
  expect(screen.getByText('连接有未保存修改，请先保存')).toBeTruthy()
})

it('shows classifier health next to the debugging workflow', () => {
  render(<JevSettingsPage config={{ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, timeout_ms: 5000, updated_at: '' }} runtime={{ classifier: 'ok', last_checked_at: '2026-09-23T01:00:00Z', last_error_kind: '' }} onSave={vi.fn()} onTest={vi.fn()} />)
  expect(screen.getByText('分类器正常')).toBeTruthy()
  expect((screen.getByLabelText('分类器超时（毫秒）') as HTMLInputElement).value).toBe('5000')
  expect(screen.queryByText(/先保存模型连接/)).toBeNull()
})

it('does not show a last-updated explanation beside the connection form', () => {
  render(<JevSettingsPage config={{ base_url: 'https://api.example/v1', model: 'jev-test', api_key_set: true, timeout_ms: 5000, updated_at: '2026-09-23T08:13:28Z' }} onSave={vi.fn()} onTest={vi.fn()} />)
  expect(screen.queryByText(/更新于/)).toBeNull()
})
