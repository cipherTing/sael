import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SettingsPage from './SettingsPage'
import type { PolicyResponse } from './policy'

const policy: PolicyResponse = {
  enabled: true, version: 5, thresholds: { a: 0.5, b: 0.5 }, preview_chars: 0, retention_days: 30,
  unmatched_action: 'allow', questions: [{ key: 'a', type: 'noul', max: 1 }, { key: 'b', type: 'noul', max: 1 }],
  scenes: [
    { id: 'a', name: '规则 A', questions: ['a'], match: 'any', action: 'block' },
    { id: 'b', name: '规则 B', questions: ['a','b'], match: 'all', action: 'allow' }
  ]
}

afterEach(cleanup)

describe('settings', () => {
	 it('gives each threshold input an accessible name', () => {
	   render(<SettingsPage policy={policy} onSave={vi.fn()} />)
	   expect(screen.getByRole('spinbutton', { name: 'a 阈值' })).toBeTruthy()
	   expect(screen.queryByText('配置完成度')).toBeNull()
	 })
	 it('shows inline errors before an empty scene can be saved', () => {
	   const onSave = vi.fn()
	   render(<SettingsPage policy={policy} onSave={onSave} />)
	   fireEvent.click(screen.getByRole('button', { name: '新增场景' }))
	   fireEvent.click(screen.getByRole('button', { name: '保存并生效' }))
	   expect(screen.getByText('请填写场景名称')).toBeTruthy()
	   expect(screen.getByText('至少选择一个审核项')).toBeTruthy()
	   expect(onSave).not.toHaveBeenCalled()
	 })

	 it('does not describe a newly added scene as a priority reorder', () => {
	   render(<SettingsPage policy={policy} onSave={vi.fn().mockResolvedValue(undefined)} />)
	   fireEvent.click(screen.getByRole('button', { name: '新增场景' }))
	   fireEvent.change(screen.getByRole('textbox', { name: '场景名称' }), { target: { value: '新场景' } })
	   fireEvent.click(screen.getByRole('checkbox', { name: 'a' }))
	   fireEvent.click(screen.getByRole('button', { name: '保存并生效' }))
	   expect(screen.queryByText('• 调整场景优先级')).toBeNull()
	 })
  it('saves reordered scenes as a whole policy after confirmation', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<SettingsPage policy={policy} onSave={onSave} />)
    fireEvent.dragStart(screen.getByLabelText('拖动场景：规则 B'))
    fireEvent.dragOver(screen.getByLabelText('放置到场景：规则 A'))
    fireEvent.drop(screen.getByLabelText('放置到场景：规则 A'))
    fireEvent.click(screen.getByRole('button', { name: '保存并生效' }))
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ version: 5, scenes: [expect.objectContaining({ id: 'b' }), expect.objectContaining({ id: 'a' })] }))
  })
})
