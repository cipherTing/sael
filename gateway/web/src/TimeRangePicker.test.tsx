import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TimeRangePicker, type TimeRangeValue } from './TimeRangePicker'

afterEach(cleanup)

const current: TimeRangeValue = { key: '24h', label: '近 24 小时', hours: 24 }

it('applies a quick range immediately when it is clicked', () => {
  const onChange = vi.fn()
  render(<TimeRangePicker value={current} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: '时间范围：近 24 小时' }))
  fireEvent.click(screen.getByRole('button', { name: '近 7 天' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ key: '7d', hours: 168, label: '近 7 天' }))
})

it('waits for apply before submitting a custom start and end date', () => {
  const onChange = vi.fn()
  render(<TimeRangePicker value={current} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: '时间范围：近 24 小时' }))
  fireEvent.change(screen.getByLabelText('开始日期'), { target: { value: '2026-09-23' } })
  fireEvent.change(screen.getByLabelText('结束日期'), { target: { value: '2026-09-24' } })
  expect(onChange).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '应用' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ key: 'custom', startDate: '2026-09-23', endDate: '2026-09-24' }))
})
