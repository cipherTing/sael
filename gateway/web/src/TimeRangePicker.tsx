import { useState } from 'react'
import { ArrowForwardOutlined, CalendarMonthOutlined } from '@mui/icons-material'
import { Box, Button, Divider, Paper, Popover, Stack, TextField, Typography } from '@mui/material'

export type TimeRangeValue = {
  key: string
  label: string
  hours: number
  start?: string
  end?: string
  startDate?: string
  endDate?: string
}

function dateInputValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function localDay(value: string) {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(year, month - 1, day)
}

function exactRange(key: string, label: string, startDate: string, endDate: string): TimeRangeValue {
  const start = localDay(startDate)
  const end = localDay(endDate)
  end.setDate(end.getDate() + 1)
  return {
    key,
    label,
    hours: Math.max(1, Math.round((end.getTime() - start.getTime()) / 3_600_000)),
    start: start.toISOString(),
    end: end.toISOString(),
    startDate,
    endDate
  }
}

function quickRanges(now = new Date()): TimeRangeValue[] {
  const today = dateInputValue(now)
  const todayDate = localDay(today)
  const yesterdayDate = new Date(todayDate)
  yesterdayDate.setDate(yesterdayDate.getDate() - 1)
  const yesterday = dateInputValue(yesterdayDate)
  const monthStart = new Date(todayDate.getFullYear(), todayDate.getMonth(), 1)
  const nextMonthStart = new Date(todayDate.getFullYear(), todayDate.getMonth() + 1, 1)
  const previousMonthStart = new Date(todayDate.getFullYear(), todayDate.getMonth() - 1, 1)
  const monthEnd = new Date(nextMonthStart)
  monthEnd.setDate(monthEnd.getDate() - 1)
  const previousMonthEnd = new Date(monthStart)
  previousMonthEnd.setDate(previousMonthEnd.getDate() - 1)
  return [
    exactRange('today', '今天', today, today),
    exactRange('yesterday', '昨天', yesterday, yesterday),
    { key: '1h', label: '近 1 小时', hours: 1 },
    { key: '6h', label: '近 6 小时', hours: 6 },
    { key: '12h', label: '近 12 小时', hours: 12 },
    { key: '24h', label: '近 24 小时', hours: 24 },
    { key: '7d', label: '近 7 天', hours: 168 },
    { key: '14d', label: '近 14 天', hours: 336 },
    { key: '30d', label: '近 30 天', hours: 720 },
    exactRange('month', '本月', dateInputValue(monthStart), dateInputValue(monthEnd)),
    exactRange('last-month', '上月', dateInputValue(previousMonthStart), dateInputValue(previousMonthEnd))
  ]
}

function defaultDraft(value: TimeRangeValue) {
  const end = new Date()
  const start = new Date(end.getTime() - Math.min(value.hours, 24) * 3_600_000)
  return { start: value.startDate || dateInputValue(start), end: value.endDate || dateInputValue(end) }
}

export function TimeRangePicker({ value, onChange }: { value: TimeRangeValue; onChange: (value: TimeRangeValue) => void }) {
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const draft = defaultDraft(value)
  const [startDate, setStartDate] = useState(draft.start)
  const [endDate, setEndDate] = useState(draft.end)
  const open = Boolean(anchorEl)
  const ranges = quickRanges()
  const customValid = Boolean(startDate && endDate && endDate >= startDate)

  function openPicker(event: React.MouseEvent<HTMLElement>) {
    const next = defaultDraft(value)
    setStartDate(next.start)
    setEndDate(next.end)
    setAnchorEl(event.currentTarget)
  }

  function applyCustom() {
    if (!customValid) return
    onChange(exactRange('custom', `${startDate.replaceAll('-', '/')} – ${endDate.replaceAll('-', '/')}`, startDate, endDate))
    setAnchorEl(null)
  }

  return <>
    <Button aria-label={`时间范围：${value.label}`} variant="outlined" size="small" startIcon={<CalendarMonthOutlined />} onClick={openPicker} sx={{ minWidth: 150, justifyContent: 'flex-start', whiteSpace: 'nowrap' }}>
      {value.label}
    </Button>
    <Popover open={open} anchorEl={anchorEl} onClose={() => setAnchorEl(null)} anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }} transformOrigin={{ vertical: 'top', horizontal: 'right' }}>
      <Paper sx={{ width: { xs: 320, sm: 420 }, p: 1.5 }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 0.5 }}>
          {ranges.map(range => <Button key={range.key} size="small" color={range.key === value.key ? 'primary' : 'inherit'} variant={range.key === value.key ? 'contained' : 'text'} onClick={() => { onChange(range); setAnchorEl(null) }} sx={{ minHeight: 36, justifyContent: 'center' }}>{range.label}</Button>)}
        </Box>
        <Divider sx={{ my: 1.25 }} />
        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' } }}>
          <TextField size="small" type="date" label="开始日期" value={startDate} onChange={event => setStartDate(event.target.value)} slotProps={{ inputLabel: { shrink: true } }} fullWidth />
          <ArrowForwardOutlined sx={{ color: 'text.secondary', display: { xs: 'none', sm: 'block' } }} />
          <TextField size="small" type="date" label="结束日期" value={endDate} onChange={event => setEndDate(event.target.value)} slotProps={{ inputLabel: { shrink: true } }} fullWidth />
          <Button variant="contained" onClick={applyCustom} disabled={!customValid} sx={{ minWidth: 72 }}>应用</Button>
        </Stack>
        {!customValid && <Typography variant="caption" color="error" sx={{ display: 'block', mt: 0.75 }}>结束日期不能早于开始日期</Typography>}
      </Paper>
    </Popover>
  </>
}
