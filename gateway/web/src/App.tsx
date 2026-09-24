import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { Link, Route, Routes, useLocation, useNavigate } from 'react-router'
import {
  Alert, AppBar, Box, Button, CircularProgress, Divider, List, ListItemButton, ListItemIcon,
  ListItemText, Paper, Stack, Tab, Tabs, TextField, Toolbar, Typography
} from '@mui/material'
import InsightsOutlined from '@mui/icons-material/InsightsOutlined'
import RuleOutlined from '@mui/icons-material/RuleOutlined'
import TuneOutlined from '@mui/icons-material/TuneOutlined'
import LogoutOutlined from '@mui/icons-material/LogoutOutlined'
import ShieldOutlined from '@mui/icons-material/ShieldOutlined'
import { request } from './api'
import type { EventFilters } from './EventsPage'
import type { Policy, PolicyResponse } from './policy'
import type { UpstreamConfig } from './SettingsPage'
import type { Event, Overview } from './types'
import type { JevConfig, JevInput, JevRuntime, JevTestResult } from './JevSettingsPage'
import type { TimeRangeValue } from './TimeRangePicker'

const OverviewPage = lazy(() => import('./OverviewPage'))
const EventsPage = lazy(() => import('./EventsPage'))
const SettingsPage = lazy(() => import('./SettingsPage'))
const JevSettingsPage = lazy(() => import('./JevSettingsPage'))

const defaultOverviewRange: TimeRangeValue = { key: '24h', label: '近 24 小时', hours: 24 }

function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError('')
    try { await request('/admin/login', { method: 'POST', body: JSON.stringify({ password }) }); onLogin() }
    catch { setError('管理员密码不正确') }
    finally { setBusy(false) }
  }
  return <Box sx={{ minHeight: '100vh', display: 'grid', placeItems: 'center', bgcolor: '#f6f7f9', p: 2 }}>
    <Paper variant="outlined" sx={{ width: '100%', maxWidth: 410, p: 4, borderRadius: 3 }}>
      <Stack spacing={2.5} component="form" onSubmit={submit}>
        <Stack direction="row" spacing={1.5} sx={{ alignItems: 'center' }}><ShieldOutlined color="primary" fontSize="large" /><Typography variant="h4" sx={{ fontWeight: 750 }}>Sael</Typography></Stack>
        <Typography variant="h6">登录运维平台</Typography>
        {error && <Alert severity="error">{error}</Alert>}
        <TextField label="管理员密码" type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} required fullWidth autoFocus />
        <Button type="submit" variant="contained" size="large" disabled={busy}>登录</Button>
      </Stack>
    </Paper>
  </Box>
}

const nav = [
  { path: '/', label: '总览', icon: <InsightsOutlined /> },
  { path: '/events', label: '命中记录', icon: <RuleOutlined /> },
  { path: '/settings', label: '设置', icon: <TuneOutlined /> }
]

export default function App() {
  const location = useLocation()
  const navigate = useNavigate()
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [policy, setPolicy] = useState<PolicyResponse | null>(null)
  const [upstream, setUpstream] = useState<UpstreamConfig | null>(null)
  const [overview, setOverview] = useState<Overview | null>(null)
  const [overviewError, setOverviewError] = useState('')
  const [overviewRange, setOverviewRange] = useState<TimeRangeValue>(defaultOverviewRange)
  const [refreshToken, setRefreshToken] = useState(0)
  const [events, setEvents] = useState<Event[]>([])
  const [activeFilters, setActiveFilters] = useState<EventFilters>(() => {
    const query = new URLSearchParams(window.location.search)
    return { hours: 24, kind: query.get('kind') || '', action: query.get('action') || '', search: '' }
  })
  const [eventsLoading, setEventsLoading] = useState(false)
  const [eventsError, setEventsError] = useState('')
  const [jev, setJev] = useState<JevConfig | null>(null)
  const [jevError, setJevError] = useState('')
  const [settingsTab, setSettingsTab] = useState(0)
  const [runtime, setRuntime] = useState<(JevRuntime & { upstream_url?: string }) | null>(null)
  useEffect(() => { request('/admin/session').then(() => setAuthenticated(true)).catch(() => setAuthenticated(false)) }, [])
  useEffect(() => {
    if (!authenticated) return
    request<PolicyResponse>('/admin/policy').then(setPolicy).catch(error => setOverviewError(String(error)))
    request<UpstreamConfig>('/admin/upstream').then(setUpstream).catch(() => setUpstream({ base_url: '', updated_at: '' }))
    request<JevConfig>('/admin/jev').then(setJev).catch(error => setJevError(String(error)))
  }, [authenticated])
  useEffect(() => {
    if (!authenticated) return
    setOverviewError('')
    const query = new URLSearchParams()
    if (overviewRange.start && overviewRange.end) {
      query.set('start', overviewRange.start)
      query.set('end', overviewRange.end)
    } else {
      query.set('hours', String(overviewRange.hours))
    }
    request<Overview>(`/admin/overview?${query}`).then(setOverview).catch(error => setOverviewError(String(error)))
    request<typeof runtime>('/admin/runtime').then(setRuntime).catch(() => {})
  }, [authenticated, overviewRange, refreshToken])

  const loadEvents = useCallback(async (filters: EventFilters, offset = 0) => {
    setEventsLoading(true); setEventsError('')
    const q = new URLSearchParams({ hours: String(filters.hours), kind: filters.kind, action: filters.action, search: filters.search, offset: String(offset) })
    try {
      const rows = await request<Event[]>(`/admin/events?${q}`)
      setEvents(current => offset ? [...current, ...rows] : rows)
    } catch (error) { setEventsError(String(error)) }
    finally { setEventsLoading(false) }
  }, [])
  useEffect(() => {
    if (!authenticated || location.pathname !== '/events') return
    const q = new URLSearchParams(location.search)
    const filters = { hours: 24, kind: q.get('kind') || '', action: q.get('action') || '', search: q.get('search') || '' }
    setActiveFilters(filters)
    void loadEvents(filters)
  }, [authenticated, location.pathname, location.search, loadEvents])
  useEffect(() => {
    if (location.pathname !== '/settings') return
    const value = Number(new URLSearchParams(location.search).get('tab'))
    if (Number.isInteger(value) && value >= 0 && value <= 1) setSettingsTab(value)
  }, [location.pathname, location.search])

  async function savePolicy(next: Policy) {
    const saved = await request<Policy>('/admin/policy', { method: 'PUT', body: JSON.stringify(next) })
    setPolicy({ ...saved, questions: policy?.questions || [] })
    setRefreshToken(value => value + 1)
  }
  async function saveJev(input: JevInput) {
    const saved = await request<JevConfig>('/admin/jev', { method: 'PUT', body: JSON.stringify(input) })
    setJev(saved)
    setRefreshToken(value => value + 1)
    return saved
  }
  async function saveUpstream(input: { base_url: string }) {
    const saved = await request<UpstreamConfig>('/admin/upstream', { method: 'PUT', body: JSON.stringify(input) })
    setUpstream(saved)
    return saved
  }
  async function testJev(text: string) {
    const result = await request<JevTestResult>('/admin/jev/test', { method: 'POST', body: JSON.stringify({ text }) })
    setRefreshToken(value => value + 1)
    return result
  }
  async function logout() {
    try { await request('/admin/logout', { method: 'POST' }) } finally { setAuthenticated(false); setPolicy(null) }
  }
  if (authenticated === null) return <Box sx={{ minHeight: '100vh', display: 'grid', placeItems: 'center' }}><CircularProgress /></Box>
  if (!authenticated) return <Login onLogin={() => setAuthenticated(true)} />
  const current = nav.find(item => item.path === location.pathname)?.path || '/'
  return <Box sx={{ minHeight: '100vh', bgcolor: '#f7f8fa' }}>
    <Box component="aside" sx={{ display: { xs: 'none', md: 'block' }, position: 'fixed', width: 232, top: 0, bottom: 0, bgcolor: '#fff', borderRight: '1px solid #e4e8ee', zIndex: 2 }}>
      <Stack direction="row" spacing={1.2} sx={{ alignItems: 'center',  px: 3, height: 74 }}><ShieldOutlined color="primary" /><Typography variant="h5" sx={{ fontWeight: 750, letterSpacing: '-.04em' }}>Sael</Typography></Stack>
      <Divider /><List sx={{ px: 1.5, py: 2 }}>{nav.map(item => <ListItemButton key={item.path} component={Link} to={item.path} selected={current === item.path} sx={{ borderRadius: 2, mb: 0.5 }}><ListItemIcon sx={{ minWidth: 38 }}>{item.icon}</ListItemIcon><ListItemText primary={item.label} /></ListItemButton>)}</List>
      <Stack spacing={0.25} sx={{ position: 'absolute', left: 18, right: 18, bottom: 18, pt: 1.5, borderTop: '1px solid #e4e8ee' }}>
        <Button component={Link} to="/settings?tab=0" size="small" color={policy?.enabled ? 'success' : 'warning'} sx={{ justifyContent: 'flex-start', fontWeight: 650 }}>{policy?.enabled ? '● 审查中' : '● 透传中'}</Button>
        {runtime?.classifier === 'error' && <Button component={Link} to="/settings?tab=1" size="small" color="error" sx={{ justifyContent: 'flex-start' }}>● 分类器异常</Button>}
        {!jev?.api_key_set && <Button component={Link} to="/settings?tab=1" size="small" color="warning" sx={{ justifyContent: 'flex-start' }}>● 分类器未配置</Button>}
        <Button onClick={() => void logout()} startIcon={<LogoutOutlined />} size="small" color="inherit" sx={{ justifyContent: 'flex-start' }}>退出</Button>
      </Stack>
    </Box>
    <Box sx={{ ml: { md: '232px' } }}>
      <AppBar position="sticky" color="inherit" elevation={0} sx={{ display: { md: 'none' }, borderBottom: '1px solid #e4e8ee' }}><Toolbar sx={{ gap: 1.5, minHeight: 56 }}>
        <ShieldOutlined color="primary" /><Typography variant="subtitle1" sx={{ fontWeight: 750, mr: 'auto' }}>Sael</Typography>
        <Typography variant="body2" color={policy?.enabled ? 'success.main' : 'warning.main'}>{policy?.enabled ? '审查中' : '透传中'}</Typography>
        <Button onClick={() => void logout()} startIcon={<LogoutOutlined />} size="small" color="inherit">退出</Button>
      </Toolbar></AppBar>
      <Stack direction="row" sx={{ display: { xs: 'flex', md: 'none' }, overflowX: 'auto', px: 2, bgcolor: '#fff', borderBottom: '1px solid #e4e8ee' }}>{nav.map(item => <Button key={item.path} component={Link} to={item.path} color={current === item.path ? 'primary' : 'inherit'} sx={{ whiteSpace: 'nowrap' }}>{item.label}</Button>)}</Stack>
      <Box component="main" sx={{ p: { xs: 2, sm: 3, lg: 4 }, maxWidth: 1500, mx: 'auto' }}>
        <Suspense fallback={<CircularProgress />}><Routes>
          <Route path="/" element={overviewError ? <Alert severity="error">{overviewError}</Alert> : overview ? <OverviewPage data={overview} enabled={Boolean(policy?.enabled)} hours={overviewRange.hours} range={overviewRange} onRangeChange={setOverviewRange} onRefresh={() => setRefreshToken(value => value + 1)} /> : <CircularProgress />} />
          <Route path="/events" element={<EventsPage events={events} loading={eventsLoading} error={eventsError} onFilter={filters => { setActiveFilters(filters); const q = new URLSearchParams({ hours: String(filters.hours), kind: filters.kind, action: filters.action, search: filters.search }); navigate(`/events?${q}`) }} onMore={() => void loadEvents(activeFilters, events.length)} />} />
          <Route path="/settings" element={policy ? <Stack spacing={2}><Paper variant="outlined" sx={{ px: 1 }}><Tabs value={settingsTab} onChange={(_, value: number) => { setSettingsTab(value); navigate(`/settings?tab=${value}`) }}><Tab label="审查策略" /><Tab label="Jev 连接与调试" /></Tabs></Paper>
            {settingsTab === 0 ? upstream ? <SettingsPage policy={policy} upstream={upstream} onSave={savePolicy} onSaveUpstream={saveUpstream} /> : <CircularProgress /> : jev ? <JevSettingsPage config={jev} runtime={runtime} onSave={saveJev} onTest={testJev} /> : jevError ? <Alert severity="error">{jevError}</Alert> : <CircularProgress />}
          </Stack> : <CircularProgress />} />
        </Routes></Suspense>
      </Box>
    </Box>
  </Box>
}
