import { useState } from 'react'
import { Alert, Box, Button, Chip, Divider, Drawer, FormControl, InputLabel, MenuItem, Paper, Select, Stack, TextField, Typography } from '@mui/material'
import { DataGrid, type GridColDef } from '@mui/x-data-grid'
import { zhCN } from '@mui/x-data-grid/locales'
import { Link } from 'react-router'
import type { Event } from './types'
import { questionName } from './questionMeta'

export type EventFilters = { hours: number; kind: string; action: string; search: string }
type Props = { events: Event[]; loading: boolean; error: string; onFilter: (filters: EventFilters) => void; onMore: () => void }

function resultLabel(event: Event) {
  if (event.kind === 'failure') return '未审放行'
  return event.decision.action === 'block' ? '已拦截' : '仅记录并转发'
}

export default function EventsPage({ events, loading, error, onFilter, onMore }: Props) {
  const query = new URLSearchParams(window.location.search)
  const [filters, setFilters] = useState<EventFilters>({ hours: 24, kind: query.get('kind') || '', action: query.get('action') || '', search: '' })
  const [selected, setSelected] = useState<Event | null>(null)
  const [copied, setCopied] = useState(false)
  const columns: GridColDef<Event>[] = [
    { field: 'time', headerName: '时间', width: 155, valueFormatter: value => new Date(value).toLocaleString('zh-CN') },
    { field: 'result', headerName: '处理结果', width: 125, renderCell: params => <Chip size="small" color={params.row.kind === 'failure' ? 'warning' : params.row.decision.action === 'block' ? 'error' : 'default'} label={resultLabel(params.row)} /> },
    { field: 'hits', headerName: '命中项', width: 135, valueGetter: (_, row) => row.decision.hits?.map(hit => questionName(hit.question)).join('、') || '—' },
    { field: 'scene', headerName: '生效场景', width: 135, valueGetter: (_, row) => row.decision.scene_name || '未匹配场景' },
    { field: 'classifier_ms', headerName: '分类耗时', width: 95, valueFormatter: value => `${value || 0} ms` },
    { field: 'model', headerName: '模型', width: 90 },
    { field: 'request_id', headerName: '请求 ID', width: 180 }
  ]
  const blocked = events.filter(event => event.kind === 'hit' && event.decision.action === 'block').length
  const allowed = events.filter(event => event.kind === 'hit' && event.decision.action === 'allow').length
  const failures = events.filter(event => event.kind === 'failure').length
  return <Stack spacing={2.5}>
    <Typography variant="h4" sx={{ fontWeight: 700 }}>命中记录</Typography>
    <Paper variant="outlined" sx={{ p: 2, borderRadius: 3 }}>
      <Stack direction={{ xs: 'column', md: 'row' }} spacing={1.5} sx={{ alignItems: { md: 'center' } }}>
        <FormControl size="small" sx={{ minWidth: 125 }}><InputLabel>时间</InputLabel><Select label="时间" value={filters.hours} onChange={e => setFilters({ ...filters, hours: Number(e.target.value) })}><MenuItem value={1}>近 1 小时</MenuItem><MenuItem value={24}>近 24 小时</MenuItem><MenuItem value={168}>近 7 天</MenuItem></Select></FormControl>
        <FormControl size="small" sx={{ minWidth: 140 }}><InputLabel>记录类型</InputLabel><Select label="记录类型" value={filters.kind} onChange={e => setFilters({ ...filters, kind: e.target.value })}><MenuItem value="">全部</MenuItem><MenuItem value="hit">命中</MenuItem><MenuItem value="failure">审查失败</MenuItem></Select></FormControl>
        <FormControl size="small" sx={{ minWidth: 140 }}><InputLabel>处理结果</InputLabel><Select label="处理结果" value={filters.action} onChange={e => setFilters({ ...filters, action: e.target.value })}><MenuItem value="">全部</MenuItem><MenuItem value="allow">记录并放行</MenuItem><MenuItem value="block">记录并拦截</MenuItem></Select></FormControl>
        <TextField size="small" label="请求 ID、模型或预览关键词" value={filters.search} onChange={e => setFilters({ ...filters, search: e.target.value })} sx={{ flex: 1, minWidth: 200 }} onKeyDown={e => { if (e.key === 'Enter') onFilter(filters) }} />
        <Button variant="contained" onClick={() => onFilter(filters)}>查询</Button>
      </Stack>
    </Paper>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }}><Typography variant="body2" color="text.secondary">当前 {events.length} 条</Typography><Chip size="small" color="error" variant={blocked ? 'filled' : 'outlined'} label={`已拦截 ${blocked}`} /><Chip size="small" variant="outlined" label={`仅记录 ${allowed}`} /><Chip size="small" color="warning" variant={failures ? 'filled' : 'outlined'} label={`审查失败 ${failures}`} /></Stack>
    {error && <Alert severity="error">{error}</Alert>}
    <Paper variant="outlined" sx={{ borderRadius: 2, overflow: 'hidden' }}>
      {!loading && !events.length ? <Box sx={{ px: 3, py: 4 }}><Typography sx={{ fontWeight: 650 }}>筛选范围内没有命中或审查失败记录</Typography><Typography variant="body2" color="text.secondary">透传请求和正常未命中请求只计入总览，不生成逐条记录。可以放宽筛选条件，或前往总览查看流量。</Typography></Box> : <DataGrid rows={events} columns={columns} loading={loading} onRowClick={params => setSelected(params.row)} disableRowSelectionOnClick initialState={{ pagination: { paginationModel: { pageSize: 10 } } }} pageSizeOptions={[10, 25, 50]} autoHeight sx={{ border: 0, minHeight: 240, '& .MuiDataGrid-row': { cursor: 'pointer' } }} localeText={{ ...zhCN.components.MuiDataGrid.defaultProps.localeText, noRowsLabel: '该时间段没有记录' }} />}
    </Paper>
    {events.length >= 50 && <Button onClick={onMore} disabled={loading}>加载更多</Button>}
    <Drawer anchor="right" open={Boolean(selected)} onClose={() => setSelected(null)}><Box sx={{ width: { xs: '100vw', sm: 520 }, p: 3, boxSizing: 'border-box' }}>
      {selected && <Stack spacing={2}>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between' }}><Typography variant="h5" sx={{ fontWeight: 700 }}>事件详情</Typography><Button onClick={() => setSelected(null)}>关闭</Button></Stack>
        <Chip label={resultLabel(selected)} color={selected.kind === 'failure' ? 'warning' : selected.decision.action === 'block' ? 'error' : 'default'} sx={{ alignSelf: 'start' }} />
        <Typography><strong>生效场景：</strong>{selected.decision.scene_name || '未匹配场景'}{selected.decision.scene_priority ? ` · 优先级 ${selected.decision.scene_priority}` : ''}</Typography>
        <Typography variant="body2" color="text.secondary">策略 v{selected.policy_version} · 分类器 {selected.classifier_ms} ms · {new Date(selected.time).toLocaleString('zh-CN')}</Typography>
        <Divider />
        <Typography variant="subtitle1" sx={{ fontWeight: 650 }}>命中项和当时阈值</Typography>
        {selected.decision.hits?.length ? selected.decision.hits.map(hit => <Typography key={hit.question} variant="body2">{questionName(hit.question)}　{hit.value} &gt; {hit.threshold}</Typography>) : <Typography color="text.secondary">{selected.error_kind === 'classifier_timeout' ? '分类器超时' : selected.error_kind === 'classifier_unavailable' ? '分类器不可用或返回无效分数' : '无命中项'}</Typography>}
        {selected.decision.also_matched?.length ? <Typography variant="body2" color="text.secondary">其他匹配场景：{selected.decision.also_matched.join('、')}（优先级较低）</Typography> : null}
        <Divider />
        <Typography variant="subtitle1" sx={{ fontWeight: 650 }}>当前用户文本预览</Typography><Typography variant="body2" sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{selected.text_preview || '未保存预览'}</Typography>
        <Divider />
        <Typography variant="subtitle1" sx={{ fontWeight: 650 }}>请求参数</Typography>
        {[['协议', selected.protocol], ['端点', selected.endpoint], ['模型', selected.model || '未提供'], ['流式', selected.stream ? '是' : '否'], ['包含非文本输入', selected.has_non_text_input ? '是，仅审核文本' : '否']].map(([label, value]) => <Stack direction="row" key={label} sx={{ justifyContent: 'space-between' }}><Typography variant="body2" color="text.secondary">{label}</Typography><Typography variant="body2" sx={{ maxWidth: 310, wordBreak: 'break-all', textAlign: 'right' }}>{value}</Typography></Stack>)}
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><Typography variant="body2" color="text.secondary">请求 ID</Typography><Typography variant="body2" sx={{ flex: 1, wordBreak: 'break-all', textAlign: 'right' }}>{selected.request_id}</Typography><Button size="small" onClick={() => { void navigator.clipboard?.writeText(selected.request_id); setCopied(true); setTimeout(() => setCopied(false), 1200) }}>{copied ? '已复制' : '复制'}</Button></Stack>
        {selected.scores?.length ? <><Divider /><Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'center' }}><Typography variant="subtitle1" sx={{ fontWeight: 650 }}>全部审核分数</Typography><Button component={Link} to="/settings?tab=1" size="small">去 Jev 调试</Button></Stack><Stack spacing={0.75}>{[...selected.scores].sort((a, b) => Number(Boolean(selected.decision.hits?.find(hit => hit.question === b.question))) - Number(Boolean(selected.decision.hits?.find(hit => hit.question === a.question)))).map(score => { const hit = selected.decision.hits?.find(item => item.question === score.question); return <Stack direction="row" key={score.question} sx={{ justifyContent: 'space-between', alignItems: 'center', px: 1, py: 0.75, bgcolor: hit ? '#fff4f1' : '#f6f8fb', borderRadius: 1 }}><Typography variant="body2">{questionName(score.question)} <Typography component="span" variant="caption" color="text.secondary">{score.question}</Typography></Typography><Stack direction="row" spacing={1.5} sx={{ alignItems: 'center' }}><Typography variant="body2">{score.value}</Typography><Typography variant="caption" color={hit ? 'error.main' : 'text.secondary'}>{hit ? `阈值 ${hit.threshold} · 已命中` : '未命中'}</Typography></Stack></Stack> })}</Stack></> : null}
      </Stack>}
    </Box></Drawer>
  </Stack>
}
