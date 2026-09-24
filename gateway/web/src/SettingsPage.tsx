import { useEffect, useMemo, useState } from 'react'
import {
  Alert, Box, Button, Checkbox, Collapse, Dialog, DialogActions, DialogContent, DialogTitle,
  FormControl, FormControlLabel, IconButton, InputLabel, NativeSelect, Paper, Stack, Switch,
  Table, TableBody, TableCell, TableContainer, TableHead, TableRow, TextField, Tooltip, Typography
} from '@mui/material'
import DragIndicatorOutlined from '@mui/icons-material/DragIndicatorOutlined'
import HelpOutlineOutlined from '@mui/icons-material/HelpOutlineOutlined'
import { type Action, type Match, type Policy, type PolicyResponse, type Scene } from './policy'
import { questionMeta, questionName } from './questionMeta'
import type { PolicyChange } from './types'

type Props = { policy: PolicyResponse; changes?: PolicyChange[]; onSave: (next: Policy) => Promise<void> }

function copyPolicy(source: PolicyResponse): Policy {
  return {
    enabled: source.enabled, version: source.version, thresholds: { ...source.thresholds },
    scenes: source.scenes.map(scene => ({ ...scene, questions: [...scene.questions] })),
    unmatched_action: source.unmatched_action, preview_chars: source.preview_chars,
    retention_days: source.retention_days
  }
}

function HelpHint({ label, text }: { label: string; text: string }) {
  return <Tooltip title={text}><IconButton size="small" aria-label={label} sx={{ color: 'text.secondary' }}><HelpOutlineOutlined fontSize="small" /></IconButton></Tooltip>
}

export default function SettingsPage({ policy, changes = [], onSave }: Props) {
  const [draft, setDraft] = useState<Policy>(() => copyPolicy(policy))
  const [expanded, setExpanded] = useState<string | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [attempted, setAttempted] = useState(false)
  const [draggedSceneId, setDraggedSceneId] = useState<string | null>(null)
  useEffect(() => { setDraft(copyPolicy(policy)); setError(''); setAttempted(false) }, [policy])
  const original = useMemo(() => copyPolicy(policy), [policy])
  const dirty = JSON.stringify(draft) !== JSON.stringify(original)
  const invalidScenes = draft.scenes.filter(scene => !scene.name.trim() || scene.questions.length === 0)
  const invalidThresholds = policy.questions.some(q => draft.thresholds[q.key] !== undefined && (!Number.isFinite(draft.thresholds[q.key]) || draft.thresholds[q.key] < 0 || draft.thresholds[q.key] > q.max))
  const missingRequired = draft.enabled && (policy.questions.some(q => draft.thresholds[q.key] === undefined) || !draft.unmatched_action || draft.preview_chars === null || draft.retention_days === null)
  const invalidRecord = (draft.preview_chars !== null && (draft.preview_chars < 0 || draft.preview_chars > 10000)) || (draft.retention_days !== null && (draft.retention_days < 1 || draft.retention_days > 3650))
  const invalid = invalidScenes.length > 0 || invalidThresholds || missingRequired || invalidRecord
  const validThresholds = policy.questions.filter(q => draft.thresholds[q.key] !== undefined && Number.isFinite(draft.thresholds[q.key]) && draft.thresholds[q.key] >= 0 && draft.thresholds[q.key] <= q.max).length
  const configuredThresholds = validThresholds
  const ready = validThresholds === policy.questions.length && !invalidThresholds && draft.scenes.every(scene => scene.name.trim() && scene.questions.length > 0) && Boolean(draft.unmatched_action) && draft.preview_chars !== null && draft.retention_days !== null

  function patchScene(index: number, patch: Partial<Scene>) {
    setDraft(current => ({ ...current, scenes: current.scenes.map((item, i) => i === index ? { ...item, ...patch } : item) }))
  }
  function move(index: number, direction: -1 | 1) {
    const next = [...draft.scenes]
    const other = index + direction
    if (other < 0 || other >= next.length) return
    ;[next[index], next[other]] = [next[other], next[index]]
    setDraft({ ...draft, scenes: next })
  }
  function moveSceneBefore(targetId: string) {
    if (!draggedSceneId || draggedSceneId === targetId) return
    setDraft(current => {
      const scenes = [...current.scenes]
      const from = scenes.findIndex(scene => scene.id === draggedSceneId)
      if (from < 0 || !scenes.some(scene => scene.id === targetId)) return current
      const [dragged] = scenes.splice(from, 1)
      scenes.splice(scenes.findIndex(scene => scene.id === targetId), 0, dragged)
      return { ...current, scenes }
    })
    setDraggedSceneId(null)
  }
  function keyboardMove(index: number, direction: -1 | 1) {
    move(index, direction)
  }
  const reordered = draft.scenes.length === original.scenes.length && draft.scenes.map(x => x.id).join() !== original.scenes.map(x => x.id).join()
  const moreBlocking = draft.scenes.some(scene => scene.action === 'block' && !original.scenes.some(old => old.id === scene.id && old.action === 'block'))
  const thresholdsChanged = JSON.stringify(draft.thresholds) !== JSON.stringify(original.thresholds)
  const sceneDetailsChanged = JSON.stringify([...draft.scenes].sort((a, b) => a.id.localeCompare(b.id))) !== JSON.stringify([...original.scenes].sort((a, b) => a.id.localeCompare(b.id)))
  const unmatchedChanged = draft.unmatched_action !== original.unmatched_action
  const recordChanged = draft.preview_chars !== original.preview_chars || draft.retention_days !== original.retention_days
  const needsConfirmation = reordered || moreBlocking || thresholdsChanged || sceneDetailsChanged || unmatchedChanged || recordChanged || (original.enabled && !draft.enabled)
  const changeSummary = [
    reordered && '调整场景优先级',
    thresholdsChanged && '修改审核项阈值',
    sceneDetailsChanged && '修改场景条件或动作',
    unmatchedChanged && '修改未匹配命中处理',
    recordChanged && '修改记录设置',
    ...draft.scenes.filter(scene => scene.action === 'block' && !original.scenes.some(old => old.id === scene.id && old.action === 'block')).map(scene => `设置「${scene.name}」为拦截`),
    original.enabled && !draft.enabled && '关闭审查'
  ].filter(Boolean)

  function startSave() {
    setAttempted(true); setError('')
    if (invalid) { if (invalidScenes[0]) setExpanded(invalidScenes[0].id); return }
    if (needsConfirmation) setConfirm(true)
    else void save()
  }

  async function save() {
    setConfirm(false); setSaving(true); setError('')
    try { await onSave(draft) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') }
    finally { setSaving(false) }
  }

  return <Stack spacing={2.5}>
    <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between' }}>
      <Stack direction="row" spacing={1.5} sx={{ alignItems: 'baseline' }}><Typography variant="h5" sx={{ fontWeight: 750 }}>审查策略</Typography><Typography variant="caption" color="text.secondary">v{policy.version}</Typography></Stack>
      <FormControlLabel label={draft.enabled ? '审查中' : '审查关闭'} control={<Switch disabled={!ready && !draft.enabled} checked={draft.enabled} onChange={(_, checked) => setDraft({ ...draft, enabled: checked })} />} />
    </Stack>
    {!ready && <Alert severity="warning" sx={{ py: 0.25 }}>配置未完成：阈值 {configuredThresholds}/{policy.questions.length}，有效场景 {draft.scenes.filter(scene => scene.name.trim() && scene.questions.length > 0).length}/{draft.scenes.length}。</Alert>}

    <Box>
      <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center', mb: 1 }}><Typography variant="h6" sx={{ fontWeight: 650 }}>审核项阈值</Typography><HelpHint label="阈值判定说明" text="分数严格大于阈值才命中，等于阈值不命中。概率项 0–1，程度项 0–3。" /></Stack>
      <TableContainer component={Paper} variant="outlined"><Table size="small"><TableHead><TableRow><TableCell>审核项</TableCell><TableCell>判定说明</TableCell><TableCell>类型 / 范围</TableCell><TableCell align="right">阈值</TableCell></TableRow></TableHead><TableBody>{policy.questions.map(question => <TableRow key={question.key} hover><TableCell sx={{ minWidth: 155 }}><Typography variant="body2" sx={{ fontWeight: 700 }}>{questionName(question.key)}</Typography><Typography variant="caption" color="text.secondary">{question.key}</Typography></TableCell><TableCell sx={{ minWidth: 250 }}><Typography variant="caption" color="text.secondary">{questionMeta[question.key]?.description || (question.type === 'score' ? '程度量表' : '概率判断')}</Typography></TableCell><TableCell sx={{ whiteSpace: 'nowrap' }}><Typography variant="caption" color="text.secondary">{question.type === 'score' ? '程度' : '概率'} · 0–{question.max}</Typography></TableCell><TableCell align="right"><TextField size="small" type="number" placeholder={`0–${question.max}`} error={draft.thresholds[question.key] !== undefined && (draft.thresholds[question.key] < 0 || draft.thresholds[question.key] > question.max) || attempted && draft.enabled && draft.thresholds[question.key] === undefined} helperText={attempted && draft.enabled && draft.thresholds[question.key] === undefined ? '必填' : ' '} slotProps={{ htmlInput: { 'aria-label': `${question.key} 阈值`, min: 0, max: question.max, step: question.type === 'score' ? 1 : 0.01 } }} value={draft.thresholds[question.key] ?? ''} onChange={event => {
            const thresholds = { ...draft.thresholds }
            if (event.target.value === '') delete thresholds[question.key]
            else thresholds[question.key] = Number(event.target.value)
            setDraft({ ...draft, thresholds })
          }} sx={{ width: 100, '& .MuiFormHelperText-root': { m: 0, minHeight: 12 } }} /></TableCell></TableRow>)}</TableBody></Table></TableContainer>
    </Box>

    <Box>
      <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between',  mb: 1 }}>
        <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}><Typography variant="h6" sx={{ fontWeight: 650 }}>场景规则</Typography><HelpHint label="场景优先级说明" text="从上到下按优先级匹配，只执行第一条命中的场景。拖拽把手可调整顺序。" /></Stack>
        <Button variant="outlined" onClick={() => { const id = crypto.randomUUID(); setDraft({ ...draft, scenes: [...draft.scenes, { id, name: '', questions: [], match: 'any', action: 'allow' }] }); setExpanded(id) }}>新增场景</Button>
      </Stack>
      <Stack spacing={1}>
        {draft.scenes.length === 0 && <Alert severity="info">尚无场景。有审核项命中时，将使用下方的未匹配处理。</Alert>}
        {draft.scenes.map((scene, index) => <Paper key={scene.id} variant="outlined" aria-label={`放置到场景：${scene.name}`} onDragOver={event => event.preventDefault()} onDrop={() => moveSceneBefore(scene.id)} sx={{ p: 1.5, opacity: draggedSceneId === scene.id ? 0.55 : 1, borderColor: draggedSceneId === scene.id ? 'primary.main' : 'divider' }}>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ justifyContent: 'space-between' }}>
            <Stack direction="row" spacing={2} sx={{ alignItems: 'center',  flex: 1 }}>
              <Stack direction="row" spacing={0.25} sx={{ alignItems: 'center' }}><Typography color="text.secondary" sx={{ fontWeight: 700, minWidth: 22 }}>{index + 1}</Typography><IconButton size="small" draggable aria-label={`拖动场景：${scene.name}`} onDragStart={() => setDraggedSceneId(scene.id)} onDragEnd={() => setDraggedSceneId(null)} onKeyDown={event => { if (event.altKey && event.key === 'ArrowUp') { event.preventDefault(); keyboardMove(index, -1) } if (event.altKey && event.key === 'ArrowDown') { event.preventDefault(); keyboardMove(index, 1) } }}><DragIndicatorOutlined fontSize="small" /></IconButton></Stack>
              <Box><Typography variant="body2" sx={{ fontWeight: 700 }}>{scene.name || '未命名场景'}</Typography><Typography variant="caption" color="text.secondary">{scene.questions.length ? scene.questions.map(questionName).join('、') : '未选择审核项'} · {scene.match === 'all' ? '全部命中' : '任一命中'} → {scene.action === 'block' ? '拦截' : '记录放行'}</Typography></Box>
            </Stack>
            <Stack direction="row" spacing={1}>
              <Button size="small" onClick={() => setExpanded(expanded === scene.id ? null : scene.id)}>{expanded === scene.id ? '收起' : '编辑'}</Button>
              <Button size="small" color="error" onClick={() => setDraft({ ...draft, scenes: draft.scenes.filter(x => x.id !== scene.id) })}>删除</Button>
            </Stack>
          </Stack>
          <Collapse in={expanded === scene.id}><Box sx={{ pt: 1.5, mt: 1.5, borderTop: '1px solid', borderColor: 'divider' }}><TextField size="small" label="场景名称" value={scene.name} onChange={e => patchScene(index, { name: e.target.value })} error={attempted && !scene.name.trim()} helperText={attempted && !scene.name.trim() ? '请填写场景名称' : ' '} sx={{ width: { xs: '100%', sm: 330 } }} />
          <Typography variant="caption" color={attempted && !scene.questions.length ? 'error.main' : 'text.secondary'} sx={{ display: 'block' }}>{attempted && !scene.questions.length ? '至少选择一个审核项' : '选择参与匹配的审核项'}</Typography>
          <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'repeat(2, 1fr)', md: 'repeat(4, 1fr)' }, my: 0.5 }}>
            {policy.questions.map(q => <FormControlLabel key={q.key} sx={{ mr: 1 }} label={<Typography variant="body2">{questionName(q.key)}</Typography>} control={<Checkbox size="small" checked={scene.questions.includes(q.key)} onChange={(_, checked) => patchScene(index, { questions: checked ? [...scene.questions, q.key] : scene.questions.filter(key => key !== q.key) })} />} />)}
          </Box>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
            <FormControl size="small" sx={{ minWidth: 170 }}><InputLabel variant="standard" htmlFor={`match-${scene.id}`}>匹配方式</InputLabel><NativeSelect id={`match-${scene.id}`} value={scene.match} onChange={e => patchScene(index, { match: e.target.value as Match })}><option value="any">满足任一项</option><option value="all">满足全部项</option></NativeSelect></FormControl>
            <FormControl size="small" sx={{ minWidth: 190 }}><InputLabel variant="standard" htmlFor={`action-${scene.id}`}>处理动作</InputLabel><NativeSelect id={`action-${scene.id}`} value={scene.action} onChange={e => patchScene(index, { action: e.target.value as Action })}><option value="allow">记录并放行</option><option value="block">记录并拦截</option></NativeSelect></FormControl>
          </Stack></Box></Collapse>
        </Paper>)}
      </Stack>
    </Box>

    <Paper variant="outlined" sx={{ p: 2 }}>
      <Typography variant="h6">未匹配命中处理</Typography>
      <FormControl fullWidth size="small" error={attempted && draft.enabled && !draft.unmatched_action}><InputLabel variant="standard" htmlFor="unmatched">处理动作</InputLabel><NativeSelect id="unmatched" value={draft.unmatched_action} onChange={e => setDraft({ ...draft, unmatched_action: e.target.value as Action })}><option value="">请选择</option><option value="allow">记录并放行</option><option value="block">记录并拦截</option></NativeSelect></FormControl>
    </Paper>

    <Paper variant="outlined" sx={{ p: 2 }}>
      <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center', mb: 1 }}><Typography variant="h6">记录设置</Typography><HelpHint label="文本预览说明" text="文本预览先去除常见邮箱和密钥，再按字符数截取。设为 0 可不保存预览。" /></Stack>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
        <TextField label="去敏预览字符数" type="number" value={draft.preview_chars ?? ''} onChange={e => setDraft({ ...draft, preview_chars: e.target.value === '' ? null : Number(e.target.value) })} slotProps={{ htmlInput: { min: 0, max: 10000 } }} />
        <TextField label="事件保留天数" type="number" value={draft.retention_days ?? ''} onChange={e => setDraft({ ...draft, retention_days: e.target.value === '' ? null : Number(e.target.value) })} slotProps={{ htmlInput: { min: 1, max: 3650 } }} />
      </Stack>
    </Paper>

    <Paper variant="outlined" sx={{ p: 2 }}><Typography variant="h6" sx={{ mb: 1.5 }}>最近策略修改</Typography>{changes.length ? <Stack spacing={0.75}>{changes.slice(0, 5).map(change => <Stack direction={{ xs: 'column', sm: 'row' }} key={change.version} spacing={{ sm: 1 }} sx={{ justifyContent: 'space-between' }}><Typography variant="body2">v{change.version} · {change.actor}</Typography><Typography variant="body2" color="text.secondary">{new Date(change.time).toLocaleString('zh-CN')}</Typography></Stack>)}</Stack> : <Typography variant="body2" color="text.secondary">暂无修改记录</Typography>}</Paper>

    <Stack direction="row" spacing={2} sx={{ justifyContent: 'flex-end', bgcolor: 'background.paper', pt: dirty ? 1.5 : 0, borderTop: dirty ? '1px solid' : 'none', borderColor: 'divider' }}>
      <Box sx={{ mr: 'auto' }}>{error ? <Alert severity="error" sx={{ py: 0 }}>{error}</Alert> : attempted && invalid ? <Alert severity="error" sx={{ py: 0 }}>请修正标红字段和未完成的场景后再保存</Alert> : dirty ? <Typography color="warning.main">有未保存的改动</Typography> : <Typography color="text.secondary">配置已保存</Typography>}</Box>
      <Button disabled={!dirty || saving} onClick={() => setDraft(copyPolicy(policy))}>放弃</Button>
      <Button variant="contained" disabled={!dirty || saving} onClick={startSave}>{saving ? '保存中…' : '保存并生效'}</Button>
    </Stack>
    <Dialog open={confirm} onClose={() => setConfirm(false)}><DialogTitle>确认策略变更</DialogTitle><DialogContent><Typography variant="body2">保存后新请求立即使用以下变更：</Typography>{changeSummary.map(change => <Typography key={String(change)} variant="body2">• {change}</Typography>)}</DialogContent><DialogActions><Button onClick={() => setConfirm(false)}>取消</Button><Button variant="contained" onClick={() => void save()}>确认保存</Button></DialogActions></Dialog>
  </Stack>
}
