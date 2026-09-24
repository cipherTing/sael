import { Box, Stack, Typography } from '@mui/material'
import {
  ArcElement, BarElement, CategoryScale, Chart as ChartJS, Filler, Legend,
  LineElement, LinearScale, PointElement, Tooltip, type ChartOptions
} from 'chart.js'
import { Bar, Doughnut, Line } from 'react-chartjs-2'
import { buildRequestSeries, type TrafficBuckets } from './overviewData'

ChartJS.register(ArcElement, BarElement, CategoryScale, Filler, Legend, LineElement, LinearScale, PointElement, Tooltip)

export const outcomeMeta = [
  { key: 'clean', name: '正常放行', color: '#4f78d1' },
  { key: 'hit_allowed', name: '命中记录', color: '#9270d6' },
  { key: 'blocked', name: '拦截', color: '#e16a70' },
  { key: 'unreviewed', name: '审查失败转发', color: '#e6a14b' },
  { key: 'disabled', name: '透传', color: '#4fb4a8' },
  { key: 'no_text', name: '无文本', color: '#a9b6c8' },
  { key: 'invalid_json', name: '请求格式错误', color: '#79879b' },
  { key: 'request_error', name: '请求读取失败', color: '#a9b6c8' },
  { key: 'gateway_error', name: '网关处理失败', color: '#d38c7b' }
] as const

const grid = '#edf1f6'
const tick = '#8793a5'
const tooltipStyle = { backgroundColor: '#202c40', padding: 11, cornerRadius: 8, displayColors: true, usePointStyle: true, bodySpacing: 5 } as const

function formatRange(range: TrafficBuckets['ranges'][number]) {
  const start = new Date(range.start)
  const end = new Date(range.end)
  const date = (value: Date) => value.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })
  const time = (value: Date) => value.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  return `${date(start)} ${time(start)}–${date(start) === date(end) ? '' : `${date(end)} `}${time(end)}`
}

const timeScales: ChartOptions<'line'>['scales'] = {
  x: { grid: { display: false }, border: { display: false }, ticks: { color: tick, maxTicksLimit: 6, minRotation: 35, maxRotation: 35, autoSkip: true, font: { size: 10 } } },
  y: { beginAtZero: true, border: { display: false, dash: [4, 4] }, grid: { color: grid, drawTicks: false }, ticks: { color: tick, precision: 0, padding: 8 } }
}

function detailedTimeScales(labels: string[]): ChartOptions<'line'>['scales'] {
  return {
    x: { offset: true, grid: { color: '#f1f4f8', drawTicks: false }, border: { display: false }, ticks: { color: tick, autoSkip: false, minRotation: 84, maxRotation: 84, align: 'inner', padding: 7, font: { size: 11 }, callback: value => labels[Number(value)] } },
    y: timeScales?.y
  }
}

const lineOptions: ChartOptions<'line'> = {
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  plugins: {
    legend: { position: 'top', align: 'end', labels: { usePointStyle: true, pointStyle: 'circle', boxWidth: 7, boxHeight: 7, color: '#66748a', padding: 16, font: { size: 11 } } },
    tooltip: tooltipStyle
  },
  scales: timeScales
}

export function RequestTrendChart({ buckets }: { buckets: TrafficBuckets }) {
  const series = buildRequestSeries(buckets)
  const datasets = [
    { label: '总请求', data: series.total, borderColor: '#4f78d1', backgroundColor: 'rgba(79,120,209,.11)', fill: true, borderWidth: 2.5, tension: 0.3, pointRadius: series.total.map(value => value ? 3 : 0), pointHoverRadius: 5 },
    ...(series.blocked.some(Boolean) ? [{ label: '拦截', data: series.blocked, borderColor: '#e16a70', backgroundColor: '#e16a70', fill: false, borderWidth: 2, tension: 0.3, pointRadius: series.blocked.map(value => value ? 3 : 0), pointHoverRadius: 5 }] : []),
    ...(series.unreviewed.some(Boolean) ? [{ label: '审查失败转发', data: series.unreviewed, borderColor: '#e6a14b', backgroundColor: '#e6a14b', fill: false, borderWidth: 2, tension: 0.3, pointRadius: series.unreviewed.map(value => value ? 3 : 0), pointHoverRadius: 5 }] : [])
  ]
  return <Box data-testid="timeline-scroll" sx={{ width: '100%', overflowX: 'auto', overflowY: 'hidden' }}><Box sx={{ minWidth: Math.max(680, buckets.axisLabels.length * 25), height: 325, position: 'relative' }}><Line aria-label="请求量趋势图" data={{ labels: buckets.axisLabels, datasets }} options={{ ...lineOptions, scales: detailedTimeScales(buckets.axisLabels), plugins: { ...lineOptions.plugins, tooltip: { ...tooltipStyle, filter: item => item.datasetIndex === 0 || (item.parsed.y ?? 0) > 0, callbacks: { title: items => items.length ? formatRange(buckets.ranges[items[0].dataIndex]) : '', label: item => `${item.dataset.label}: ${(item.parsed.y ?? 0).toLocaleString()} 次` } } } }} /></Box></Box>
}

export function SparseActivityChart({ buckets }: { buckets: TrafficBuckets }) {
  const series = buildRequestSeries(buckets)
  const active = series.total.filter(value => value > 0).length
  const peak = Math.max(0, ...series.total)
  const datasets = outcomeMeta.filter(item => buckets.counts[item.key]?.some(value => value > 0)).map(item => ({
    label: item.name,
    data: buckets.counts[item.key],
    backgroundColor: item.color,
    borderRadius: 3,
    barThickness: 18,
    stack: 'requests'
  }))

  return <Stack spacing={0.5} sx={{ height: 325 }}>
    <Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'baseline' }}>
      <Typography variant="body2" color="text.secondary">峰值 <Typography component="span" sx={{ fontWeight: 750, color: 'text.primary', fontSize: 22 }}>{peak}</Typography> 次 / {buckets.granularity}</Typography>
      <Typography variant="caption" color="text.secondary">{active} 个活跃时段</Typography>
    </Stack>
    <Box data-testid="timeline-scroll" sx={{ flex: 1, minHeight: 0, overflowX: 'auto', overflowY: 'hidden' }}><Box sx={{ minWidth: Math.max(680, buckets.axisLabels.length * 25), height: '100%' }}>
      <Bar aria-label="请求量分时图" data={{ labels: buckets.axisLabels, datasets }} options={{ responsive: true, maintainAspectRatio: false, interaction: { mode: 'index', intersect: false }, plugins: { legend: { position: 'top', align: 'end', labels: { usePointStyle: true, pointStyle: 'circle', boxWidth: 7, boxHeight: 7, color: '#66748a', padding: 14, font: { size: 11 } } }, tooltip: { ...tooltipStyle, filter: item => series.total[item.dataIndex] === 0 || (item.parsed.y ?? 0) > 0, callbacks: { title: items => items.length ? formatRange(buckets.ranges[items[0].dataIndex]) : '', label: item => `${item.dataset.label}: ${(item.parsed.y ?? 0).toLocaleString()} 次`, footer: items => `共 ${series.total[items[0].dataIndex].toLocaleString()} 次` } } }, scales: { x: { ...detailedTimeScales(buckets.axisLabels)?.x, stacked: true }, y: { ...timeScales?.y, stacked: true } } }} />
    </Box></Box>
  </Stack>
}

export function OutcomeChart({ totals }: { totals: Record<string, number> }) {
  const rows = outcomeMeta.map(item => ({ ...item, value: totals[item.key] || 0 })).filter(item => item.value > 0)
  const total = rows.reduce((sum, row) => sum + row.value, 0)
  return <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: '170px minmax(0, 1fr)' }, alignItems: 'center', gap: 1.25, minHeight: 205 }}>
    <Box sx={{ height: 184, position: 'relative' }}>
      <Doughnut aria-label="处理结果分布图" data={{ labels: rows.map(row => row.name), datasets: [{ data: rows.map(row => row.value), backgroundColor: rows.map(row => row.color), borderWidth: 0, hoverOffset: 5, spacing: 0 }] }} options={{ responsive: true, maintainAspectRatio: false, cutout: '76%', plugins: { legend: { display: false }, tooltip: { ...tooltipStyle, position: 'nearest', callbacks: { label: item => `${item.parsed.toLocaleString()} 次 · ${Math.round(item.parsed / total * 100)}%` } } } }} />
      <Stack sx={{ position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center', pointerEvents: 'none' }}><Typography variant="h4" sx={{ fontWeight: 750, lineHeight: 1 }}>{total.toLocaleString()}</Typography><Typography variant="caption" color="text.secondary">总请求</Typography></Stack>
    </Box>
    <Box sx={{ minWidth: 0 }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 54px 48px', gap: 0.5, pb: 0.5, borderBottom: '1px solid #e7ebf1' }}><Typography variant="caption" color="text.secondary">结果</Typography><Typography variant="caption" color="text.secondary" sx={{ textAlign: 'right' }}>请求</Typography><Typography variant="caption" color="text.secondary" sx={{ textAlign: 'right' }}>占比</Typography></Box>
      {rows.map(row => <Box key={row.key} sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 54px 48px', gap: 0.5, alignItems: 'center', py: 0.65, borderBottom: '1px solid #f0f2f5' }}><Stack direction="row" spacing={0.7} sx={{ alignItems: 'center', minWidth: 0 }}><Box sx={{ width: 8, height: 8, flex: '0 0 auto', borderRadius: '50%', bgcolor: row.color }} /><Typography variant="caption" sx={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.name}</Typography></Stack><Typography variant="caption" sx={{ fontWeight: 700, textAlign: 'right' }}>{row.value.toLocaleString()}</Typography><Typography variant="caption" color="text.secondary" sx={{ textAlign: 'right' }}>{Math.round(row.value / total * 100)}%</Typography></Box>)}
    </Box>
  </Box>
}

export function RankingChart({ labels, values }: { labels: string[]; values: number[] }) {
  return <Box sx={{ height: Math.max(130, labels.length * 31 + 48) }}><Bar aria-label="风险项排行图" data={{ labels, datasets: [{ label: '命中次数', data: values, backgroundColor: '#e16a70', borderRadius: 4, barThickness: 13 }] }} options={{ indexAxis: 'y', responsive: true, maintainAspectRatio: false, plugins: { legend: { display: false }, tooltip: { ...tooltipStyle, callbacks: { title: items => items.length ? labels[items[0].dataIndex] : '', label: item => `${(item.parsed.x ?? 0).toLocaleString()} 次命中` } } }, scales: { x: { beginAtZero: true, border: { display: false }, grid: { color: grid, drawTicks: false }, ticks: { color: tick, precision: 0 } }, y: { border: { display: false }, grid: { display: false }, ticks: { color: '#526076', font: { size: 11 } } } } }} /></Box>
}

export function SignalChart({ buckets, values, name, color, unit }: { buckets: TrafficBuckets; values: (number | null)[]; name: string; color: string; unit?: string }) {
  return <Box sx={{ height: 225 }}><Line aria-label={`${name}趋势图`} data={{ labels: buckets.labels, datasets: [{ label: name, data: values, borderColor: color, backgroundColor: `${color}1a`, borderWidth: 2.5, fill: true, tension: 0.3, spanGaps: false, pointRadius: values.map(value => value === null || value === 0 ? 0 : 3), pointHoverRadius: 5 }] }} options={{ ...lineOptions, scales: { ...timeScales, y: { ...timeScales?.y, ticks: { color: tick, padding: 8, precision: 0, callback: value => `${Number(value).toLocaleString()}${unit || ''}` } } }, plugins: { ...lineOptions.plugins, legend: { display: false }, tooltip: { ...tooltipStyle, filter: item => (item.parsed.y ?? 0) > 0, callbacks: { title: items => items.length ? formatRange(buckets.ranges[items[0].dataIndex]) : '', label: item => `${name}: ${(item.parsed.y ?? 0).toLocaleString()}${unit || ' 次'}` } } } }} /></Box>
}

export function FailureTrendChart({ buckets }: { buckets: TrafficBuckets }) {
  const sources = [
    { label: '审查失败转发', data: buckets.counts.unreviewed, color: '#e6a14b' }
  ].filter(source => source.data.some(value => value > 0))
  const datasets = sources.map(source => ({
    label: source.label,
    data: source.data,
    borderColor: source.color,
    backgroundColor: `${source.color}1a`,
    borderWidth: 2.5,
    fill: true,
    tension: 0.3,
    pointRadius: source.data.map(value => value ? 3 : 0),
    pointHoverRadius: 5
  }))
  return <Box sx={{ height: 225 }}><Line aria-label="异常趋势图" data={{ labels: buckets.labels, datasets }} options={{ ...lineOptions, scales: { ...timeScales, y: { ...timeScales?.y, ticks: { color: tick, padding: 8, precision: 0 } } }, plugins: { ...lineOptions.plugins, tooltip: { ...tooltipStyle, filter: item => (item.parsed.y ?? 0) > 0, callbacks: { title: items => items.length ? formatRange(buckets.ranges[items[0].dataIndex]) : '', label: item => `${item.dataset.label}: ${(item.parsed.y ?? 0).toLocaleString()} 次` } } } }} /></Box>
}
