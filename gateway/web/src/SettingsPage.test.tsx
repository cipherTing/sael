import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import SettingsPage from './SettingsPage'
import type { PolicyResponse } from './policy'

const policy: PolicyResponse = {
  enabled: true, version: 5, preview_chars: 300, retention_days: 30,
  questions: [{ key: 'gore', type: 'score', max: 3 }, { key: 'self_harm', type: 'noul', max: 1 }],
  scenes: [{ id: 'one', name: '高风险组合', note: '需要人工关注', match: 'all', action: 'block', conditions: [
    { question: 'gore', threshold: 1.5 }, { question: 'self_harm', threshold: 0.8 }
  ] }]
}

afterEach(cleanup)

it('puts scene conditions first without the old global-threshold and record forms', () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />)
  expect(screen.getByText('高风险组合')).toBeTruthy()
  expect(screen.getByText('需要人工关注')).toBeTruthy()
  expect(screen.getByText(/血腥程度.*1.5/)).toBeTruthy()
  expect(screen.getByText(/自伤风险.*0.8/)).toBeTruthy()
  for (const removed of ['审核项阈值', '未匹配命中处理', '记录设置', '最近策略修改', '类型 / 范围']) {
    expect(screen.queryByText(removed)).toBeNull()
  }
})

it('saves an original-scale threshold inside its scene', async () => {
  const onSave = vi.fn().mockResolvedValue(undefined)
  render(<SettingsPage policy={policy} onSave={onSave} />)
  fireEvent.click(screen.getByRole('button', { name: '编辑场景：高风险组合' }))
  fireEvent.change(screen.getByRole('spinbutton', { name: '血腥程度阈值' }), { target: { value: '1.8' } })
  fireEvent.click(screen.getByRole('button', { name: '保存并生效' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ scenes: [expect.objectContaining({ conditions: [
    { question: 'gore', threshold: 1.8 }, { question: 'self_harm', threshold: 0.8 }
  ] })] })))
})

it('keeps invalid new scenes beside the fields that need work', () => {
  const onSave = vi.fn()
  render(<SettingsPage policy={policy} onSave={onSave} />)
  fireEvent.click(screen.getByRole('button', { name: '新增场景' }))
  fireEvent.click(screen.getByRole('button', { name: '保存并生效' }))
  expect(screen.getByText('请填写场景名称')).toBeTruthy()
  expect(screen.getByText('至少添加一个条件')).toBeTruthy()
  expect(onSave).not.toHaveBeenCalled()
})

it('shows the forwarding destination in the settings workflow', () => {
  render(<SettingsPage policy={policy} upstream={{ base_url: 'https://api.example.test', updated_at: '' }} onSave={vi.fn()} onSaveUpstream={vi.fn()} />)
  expect((screen.getByLabelText('转发目标地址') as HTMLInputElement).value).toBe('https://api.example.test')
  expect(screen.queryByText('请求会转发到这里')).toBeNull()
})

it('keeps the rule flow causal without turning it into instructional headings', () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: '编辑场景：高风险组合' }))
  expect(screen.queryByText('发生什么')).toBeNull()
  expect(screen.queryByText('然后')).toBeNull()
  expect(screen.getAllByText('满足条件').length).toBeGreaterThan(0)
  expect(screen.getAllByText('处理方式').length).toBeGreaterThan(0)
})

it('does not add explanatory copy to the forwarding target section', () => {
  render(<SettingsPage policy={policy} upstream={{ base_url: 'https://api.example.test', updated_at: '' }} onSave={vi.fn()} onSaveUpstream={vi.fn()} />)
  expect(screen.queryByText('请求会转发到这里')).toBeNull()
})
