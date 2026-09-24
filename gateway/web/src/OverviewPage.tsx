import { Link } from 'react-router'
import { Alert, Box, Button, LinearProgress, Paper, Stack, Typography } from '@mui/material'
import TrafficOutlined from '@mui/icons-material/TrafficOutlined'
import FactCheckOutlined from '@mui/icons-material/FactCheckOutlined'
import GppMaybeOutlined from '@mui/icons-material/GppMaybeOutlined'
import BlockOutlined from '@mui/icons-material/BlockOutlined'
import type { Overview } from './types'
import { questionName } from './questionMeta'
import { buildRequestSeries, buildTrafficBuckets } from './overviewData'
import { FailureTrendChart, OutcomeChart, outcomeMeta, RankingChart, RequestTrendChart, SignalChart, SparseActivityChart } from './DashboardCharts'
import { TimeRangePicker, type TimeRangeValue } from './TimeRangePicker'

type Props = { data: Overview; enabled: boolean; hours: number; onHoursChange?: (hours: number) => void; range?: TimeRangeValue; onRangeChange?: (range: TimeRangeValue) => void; onRefresh?: () => void }

function percent(part: number, whole: number) {
  if (!whole) return '—'
  const value = part / whole * 100
  if (value > 0 && value < 0.01) return '<0.01%'
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)}%`
}

function metric(label: string, value: number | string, detail: string, icon: React.ReactNode, accent: string, tint: string, to?: string) {
  const content = <><Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><Box sx={{ width: 32, height: 32, display: 'grid', placeItems: 'center', borderRadius: 1.5, bgcolor: tint, color: accent, '& svg': { fontSize: 19 } }}>{icon}</Box><Typography variant="body2" color="text.secondary" sx={{ fontWeight: 600 }}>{label}</Typography></Stack><Typography variant="h4" sx={{ fontWeight: 750, mt: 0.8, letterSpacing: '-.04em' }}>{value}</Typography>{detail && <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.3 }}>{detail}</Typography>}</>
  const sx = { minWidth: 0, px: 1.8, py: 1.3, bgcolor: '#fff', color: 'text.primary', border: '1px solid #e5eaf1', borderRadius: 2, textDecoration: 'none', '&:hover': to ? { borderColor: accent, bgcolor: '#fbfcff' } : undefined }
  return to ? <Box key={label} data-testid="overview-kpi" component={Link} to={to} aria-label={`${label} ${value}`} sx={sx}>{content}</Box> : <Box key={label} data-testid="overview-kpi" sx={sx}>{content}</Box>
}

function Panel({ title, children, meta }: { title: string; children: React.ReactNode; meta?: string }) {
  return <Box sx={{ minWidth: 0, display: 'flex', flexDirection: 'column' }}><Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 0.75 }}><Typography variant="subtitle1" sx={{ fontWeight: 700 }}>{title}</Typography>{meta && <Typography variant="caption" color="text.secondary">{meta}</Typography>}</Stack><Paper variant="outlined" sx={{ p: 1.25, flex: 1, boxSizing: 'border-box' }}>{children}</Paper></Box>
}

function EmptyChart() {
  return <Box sx={{ height: 180, display: 'grid', placeItems: 'center' }}><Typography variant="body2" color="text.secondary">暂无数据</Typography></Box>
}

export default function OverviewPage({ data, hours, range, onRangeChange, onHoursChange, onRefresh }: Props) {
  const selectedRange = range || { key: `${hours}h`, label: hours === 24 ? '近 24 小时' : `近 ${hours} 小时`, hours }
  const buckets = buildTrafficBuckets(data.since, hours, data.trend)
  const outcomeTotals = data.trend.reduce<Record<string, number>>((totals, point) => {
    totals[point.outcome] = (totals[point.outcome] || 0) + point.count
    return totals
  }, {})
  const hasTrend = data.trend.length > 0
  const activeBuckets = buildRequestSeries(buckets).total.filter(value => value > 0).length
  const hasLatency = buckets.latency.filter(value => value !== null).length > 1
  const hasErrors = buckets.labels.filter((_, index) => buckets.counts.unreviewed[index] > 0).length > 1
  const hasOutcomes = outcomeMeta.some(item => outcomeTotals[item.key] > 0)
  const hitQuestions = data.questions.filter(item => item.count > 0).sort((a, b) => b.count - a.count).slice(0, 8)
  const scenes = data.scenes.filter(item => item.count > 0).sort((a, b) => b.count - a.count).slice(0, 6)

  return <Stack spacing={2.2}>
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5} sx={{ justifyContent: 'space-between', alignItems: { sm: 'center' } }}>
      <Typography variant="h5" sx={{ fontWeight: 750 }}>运行总览</Typography>
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><TimeRangePicker value={selectedRange} onChange={next => { if (onRangeChange) onRangeChange(next); else if (next.hours && onHoursChange) onHoursChange(next.hours) }} /><Button variant="outlined" onClick={onRefresh}>刷新</Button></Stack>
    </Stack>

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', md: 'repeat(4, minmax(0, 1fr))' }, gap: 1.25 }}>
      {metric('请求量', data.total.toLocaleString(), '', <TrafficOutlined />, '#4f78d1', '#e9f0ff')}
      {metric('实际审查', percent(data.checked, data.total), `已审 ${data.checked.toLocaleString()} / ${data.total.toLocaleString()}`, <FactCheckOutlined />, '#6977c9', '#eeefff')}
      {metric('阈值命中', data.hits.toLocaleString(), `${percent(data.hits, data.checked)} · 已审 ${data.checked.toLocaleString()}`, <GppMaybeOutlined />, '#9270d6', '#f2ecff', '/events?kind=hit')}
      {metric('策略拦截', data.blocked.toLocaleString(), `${percent(data.blocked, data.checked)} · 已审 ${data.checked.toLocaleString()}`, <BlockOutlined />, '#c66e78', '#fff0f1', '/events?action=block')}
    </Box>

    {(outcomeTotals.gateway_error > 0 || data.unreviewed > 0) && <Stack spacing={0.75}>
      {outcomeTotals.gateway_error > 0 && <Alert severity="error" sx={{ py: 0 }}><Typography component="span" variant="body2">网关处理失败 {outcomeTotals.gateway_error.toLocaleString()} 条</Typography></Alert>}
      {data.unreviewed > 0 && <Alert severity="warning" action={<Button component={Link} to="/events?kind=failure" size="small">查看记录</Button>} sx={{ py: 0 }}><Typography component="span" variant="body2">审查失败后转发 {data.unreviewed.toLocaleString()} 条</Typography><Typography component="span" variant="body2" color="text.secondary"> · 应审请求中 {percent(data.unreviewed, data.checked + data.unreviewed)}</Typography></Alert>}
    </Stack>}

    <Panel title="请求量" meta={`每 ${buckets.granularity}`}>
      {!hasTrend ? <EmptyChart /> : activeBuckets < 2 ? <SparseActivityChart buckets={buckets} /> : <RequestTrendChart buckets={buckets} />}
    </Panel>

    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', lg: 'repeat(2, minmax(0, 1fr))' }, gap: 2, alignItems: 'start' }}>
      {hasOutcomes && <Panel title="处理结果分布"><OutcomeChart totals={outcomeTotals} /></Panel>}
      {hitQuestions.length > 0 && <Panel title="风险项排行">
        {hitQuestions.length === 1 ? <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', py: 0.5 }}><Typography variant="body2">{questionName(hitQuestions[0].name)}</Typography><Typography variant="body2" sx={{ color: '#d75e69', fontWeight: 750 }}>{hitQuestions[0].count} 次</Typography></Stack> : <RankingChart labels={hitQuestions.map(item => questionName(item.name))} values={hitQuestions.map(item => item.count)} />}
      </Panel>}
      {hasLatency && <Panel title="分类器延迟" meta="毫秒">
        <SignalChart buckets={buckets} values={buckets.latency} name="平均耗时" color="#4f78d1" unit=" ms" />
      </Panel>}
      {hasErrors && <Panel title="异常趋势">
        <FailureTrendChart buckets={buckets} />
      </Panel>}
      {scenes.length > 0 && <Panel title="场景命中">
        <Stack spacing={1.25}>{scenes.map(item => <Box key={item.name}><Stack direction="row" sx={{ justifyContent: 'space-between' }}><Typography variant="body2">{item.name === 'unmatched' ? '未匹配场景' : item.name}</Typography><Typography variant="body2" sx={{ fontWeight: 700 }}>{item.count} 次</Typography></Stack>{scenes.length > 1 && <LinearProgress variant="determinate" value={data.hits ? item.count / data.hits * 100 : 0} sx={{ mt: 0.5, height: 6, borderRadius: 3 }} />}</Box>)}</Stack>
      </Panel>}
    </Box>
  </Stack>
}
