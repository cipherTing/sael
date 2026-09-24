import { useEffect, useState } from 'react'
import { Alert, Box, Button, Chip, Divider, IconButton, LinearProgress, Paper, Stack, TextField, Tooltip, Typography } from '@mui/material'
import HelpOutlineOutlined from '@mui/icons-material/HelpOutlineOutlined'
import type { Answer, Hit } from './types'
import { questionMeta, questionName } from './questionMeta'

export type JevConfig = { base_url: string; model: string; api_key_set: boolean; timeout_ms?: number; updated_at: string }
export type JevRuntime = { classifier: string; last_checked_at?: string; last_error_kind?: string }
export type JevInput = { base_url: string; model: string; api_key: string; timeout_ms: number }
export type JevTestResult = { scores: Answer[]; decision: { action: 'allow' | 'block'; hits: Hit[]; scene_name?: string; scene_priority?: number } | null; policy_ready: boolean; classifier_ms: number }
type Props = { config: JevConfig; runtime?: JevRuntime | null; onSave: (input: JevInput) => Promise<JevConfig>; onTest: (text: string) => Promise<JevTestResult> }

export default function JevSettingsPage({ config, runtime, onSave, onTest }: Props) {
  const [baseURL, setBaseURL] = useState(config.base_url)
  const [model, setModel] = useState(config.model)
  const [key, setKey] = useState('')
  const [timeoutMS, setTimeoutMS] = useState(config.timeout_ms || 5000)
  const [saved, setSaved] = useState(config)
  const [text, setText] = useState('')
  const [result, setResult] = useState<JevTestResult | null>(null)
  const [error, setError] = useState('')
  const [testError, setTestError] = useState('')
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  useEffect(() => { setSaved(config); setBaseURL(config.base_url); setModel(config.model); setTimeoutMS(config.timeout_ms || 5000) }, [config])
  let urlValid = false
  try { const u = new URL(baseURL); urlValid = ['http:', 'https:'].includes(u.protocol) && !u.username && !u.password && !u.search && !u.hash } catch { /* field error below */ }
  const canSave = urlValid && Boolean(model.trim()) && (saved.api_key_set || Boolean(key.trim())) && timeoutMS >= 1 && timeoutMS <= 120000 && !busy
  const dirty = baseURL !== saved.base_url || model !== saved.model || key !== '' || timeoutMS !== (saved.timeout_ms || 5000)
  const canTest = saved.api_key_set && !dirty && Boolean(text.trim()) && !testing

  async function save() {
    setBusy(true); setError('')
    try {
      const next = await onSave({ base_url: baseURL.trim(), model: model.trim(), api_key: key.trim(), timeout_ms: timeoutMS })
      setSaved(next); setKey('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Jev 配置保存失败') }
    finally { setBusy(false) }
  }
  async function test() {
    setTesting(true); setResult(null); setTestError('')
    try { setResult(await onTest(text.trim())) }
    catch (cause) { setTestError(cause instanceof Error ? cause.message : 'Jev 测试失败') }
    finally { setTesting(false) }
  }
  function clearTest() {
    setText('')
    setResult(null)
    setTestError('')
  }

  return <Stack spacing={2.5}>
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' }, justifyContent: 'space-between' }}>
      <Typography variant="h5" sx={{ fontWeight: 750 }}>Jev 连接与调试</Typography>
      <Tooltip title={runtime?.last_checked_at && !runtime.last_checked_at.startsWith('0001') ? `最近验证 ${new Date(runtime.last_checked_at).toLocaleString('zh-CN')}` : '尚未验证'}><Chip size="small" color={runtime?.classifier === 'ok' ? 'success' : runtime?.classifier === 'error' ? 'error' : 'default'} label={runtime?.classifier === 'ok' ? '分类器正常' : runtime?.classifier === 'error' ? '分类器异常' : '未验证'} /></Tooltip>
    </Stack>
    {runtime?.classifier === 'error' && <Alert severity="error">{runtime.last_error_kind || '分类调用失败'}</Alert>}
    <Paper variant="outlined" sx={{ p: 2.5 }}>
      <Stack direction={{ xs: 'column', md: 'row' }} spacing={1.5} sx={{ alignItems: { md: 'center' }, justifyContent: 'space-between', mb: 2 }}>
        <Typography variant="h6" sx={{ fontWeight: 650 }}>分类器连接</Typography>
        <Chip label={saved.api_key_set ? '已保存密钥' : '尚未配置'} color={saved.api_key_set ? 'success' : 'warning'} size="small" />
      </Stack>
      {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'minmax(260px, 2fr) minmax(180px, 1fr)' }, gap: 1.5 }}>
        <TextField size="small" label="接口地址" value={baseURL} onChange={e => setBaseURL(e.target.value)} error={Boolean(baseURL) && !urlValid} helperText={Boolean(baseURL) && !urlValid ? '请输入有效的 HTTP(S) API 根地址' : undefined} placeholder="https://api.typesafe.ai/v1" fullWidth />
        <TextField size="small" label="模型 ID" value={model} onChange={e => setModel(e.target.value)} placeholder="jev-latest" fullWidth />
        <TextField size="small" label="API Key" type="password" autoComplete="new-password" value={key} onChange={e => setKey(e.target.value)} helperText={saved.api_key_set ? '留空则保留当前密钥；输入新值会替换' : '首次保存必须填写'} fullWidth />
        <TextField size="small" label="分类器超时（毫秒）" type="number" value={timeoutMS} onChange={e => setTimeoutMS(Number(e.target.value))} slotProps={{ htmlInput: { min: 1, max: 120000 } }} fullWidth />
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', justifyContent: 'flex-end' }}><Button variant="contained" disabled={!canSave || !dirty} onClick={() => void save()}>保存 Jev 配置</Button></Stack>
      </Box>
    </Paper>
    <Paper variant="outlined" sx={{ p: 2.5 }}>
      <Stack direction={{ xs: 'column', md: 'row' }} spacing={1} sx={{ justifyContent: 'space-between', mb: 2 }}>
        <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}><Typography variant="h6" sx={{ fontWeight: 650 }}>文本调试</Typography><Tooltip title="调用已保存的 Jev 配置和当前策略。测试文本不会保存，不请求 AI 上游，也不计入生产统计。"><IconButton size="small" aria-label="文本调试说明"><HelpOutlineOutlined fontSize="small" /></IconButton></Tooltip></Stack>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap', justifyContent: 'flex-end' }}>{dirty && <Typography variant="caption" color="warning.main">连接有未保存修改，请先保存</Typography>}{(text || result) && <Button size="small" onClick={clearTest}>清空测试</Button>}<Button variant="contained" size="small" disabled={!canTest} onClick={() => void test()}>{testing ? '测试中…' : '测试 Jev 分类器'}</Button></Stack>
      </Stack>
      <TextField label="测试文本" multiline minRows={3} maxRows={8} fullWidth value={text} onChange={e => setText(e.target.value)} placeholder="粘贴一条当前用户请求的文本" />
      {testing && <LinearProgress sx={{ mt: 2 }} />}
      {testError && <Alert severity="error" sx={{ mt: 2 }}>{testError}</Alert>}
      {result && <Box sx={{ mt: 2 }}>
        <Divider sx={{ mb: 2 }} />
        <Stack direction={{ xs: 'column', md: 'row' }} spacing={1} sx={{ justifyContent: 'space-between', mb: 1.5 }}>
          <Typography sx={{ fontWeight: 700 }}>审核结果 <Typography component="span" variant="body2" color="text.secondary">· {result.classifier_ms} ms · {result.scores.length} 项分数</Typography></Typography>
          <Chip color={!result.policy_ready ? 'warning' : result.decision?.action === 'block' ? 'error' : 'success'} label={!result.policy_ready ? '策略未配齐：只显示原始分数' : `${result.decision?.scene_name || '未匹配场景'} · ${result.decision?.action === 'block' ? '模拟拦截' : '模拟放行'}`} />
        </Stack>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2, minmax(0, 1fr))' }, gap: 1 }}>
          {result.scores.map(score => {
            const hit = result.decision?.hits?.find(item => item.question === score.question)
            const max = score.type === 'score' ? 3 : 1
            return <Box key={score.question} sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'minmax(0, 1fr) 160px 90px' }, alignItems: 'center', gap: 1.5, px: 1.5, py: 1, bgcolor: hit ? '#fff4f1' : '#f6f8fb', borderRadius: 1 }}>
              <Tooltip title={questionMeta[score.question]?.description || ''}><Typography variant="body2" sx={{ fontWeight: 650 }}>{questionName(score.question)} <Typography component="span" variant="caption" color="text.secondary">{score.question}</Typography></Typography></Tooltip>
              <Box><LinearProgress variant="determinate" value={Math.min(100, score.value / max * 100)} color={hit ? 'error' : 'primary'} sx={{ height: 7, borderRadius: 4 }} /><Typography variant="caption" color="text.secondary">当前 {score.value} / {max}</Typography></Box>
              <Box sx={{ textAlign: { xs: 'left', sm: 'right' } }}><Typography variant="body2" sx={{ fontWeight: 750 }} color={hit ? 'error.main' : 'text.primary'}>{!result.policy_ready ? '仅原始分数' : hit ? '已命中' : '未命中'}</Typography><Typography variant="caption" color={hit ? 'error.main' : 'text.secondary'}>{!result.policy_ready ? '未配置阈值' : hit ? `阈值 ${hit.threshold}` : '低于阈值'}</Typography></Box>
            </Box>
          })}
        </Box>
      </Box>}
    </Paper>
  </Stack>
}
