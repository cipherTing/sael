import { useEffect, useMemo, useState } from 'react'
import {
  Alert, Box, Button, Chip, Collapse, FormControl, IconButton, InputLabel,
  MenuItem, Paper, Select, Stack, Switch, TextField, Tooltip, Typography
} from '@mui/material'
import AddOutlined from '@mui/icons-material/AddOutlined'
import DeleteOutlineOutlined from '@mui/icons-material/DeleteOutlineOutlined'
import DragIndicatorOutlined from '@mui/icons-material/DragIndicatorOutlined'
import EastOutlined from '@mui/icons-material/EastOutlined'
import HelpOutlineOutlined from '@mui/icons-material/HelpOutlineOutlined'
import { DndContext, PointerSensor, KeyboardSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { type Action, type Condition, type Match, type Policy, type PolicyResponse, type Question, type Scene } from './policy'
import { questionMeta, questionName } from './questionMeta'

export type UpstreamConfig = { base_url: string; updated_at: string }
type Props = { policy: PolicyResponse; upstream?: UpstreamConfig; onSave: (next: Policy) => Promise<void>; onSaveUpstream?: (next: { base_url: string }) => Promise<UpstreamConfig> }

function copyPolicy(source: PolicyResponse): Policy {
  return {
    enabled: source.enabled, version: source.version,
    scenes: source.scenes.map(scene => ({ ...scene, conditions: scene.conditions.map(condition => ({ ...condition })) })),
    preview_chars: source.preview_chars, retention_days: source.retention_days
  }
}

function validCondition(condition: Condition, questions: Question[]) {
  const question = questions.find(item => item.key === condition.question)
  return Boolean(question && Number.isFinite(condition.threshold) && condition.threshold >= 0 && condition.threshold <= question.max)
}

function validScene(scene: Scene, questions: Question[]) {
  return Boolean(scene.name.trim() && scene.conditions.length &&
    scene.conditions.every(condition => validCondition(condition, questions)) &&
    new Set(scene.conditions.map(condition => condition.question)).size === scene.conditions.length)
}

function SceneCard({ scene, index, questions, expanded, attempted, onToggle, onPatch, onDelete }: {
  scene: Scene; index: number; questions: Question[]; expanded: boolean; attempted: boolean;
  onToggle: () => void; onPatch: (patch: Partial<Scene>) => void; onDelete: () => void
}) {
  const { attributes, listeners, setActivatorNodeRef, setNodeRef, transform, transition, isDragging } = useSortable({ id: scene.id })
  const style = { transform: CSS.Transform.toString(transform), transition }
  const patchCondition = (position: number, patch: Partial<Condition>) => {
    onPatch({ conditions: scene.conditions.map((item, i) => i === position ? { ...item, ...patch } : item) })
  }
  return <Paper ref={setNodeRef} style={style} variant="outlined" sx={{ borderRadius: 2, borderColor: isDragging ? 'primary.main' : 'divider', opacity: isDragging ? 0.65 : 1, bgcolor: 'background.paper' }}>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'flex-start', px: 1.25, py: 1.1 }}>
      <IconButton ref={setActivatorNodeRef} {...attributes} {...listeners} size="small" aria-label={`拖动场景：${scene.name || index + 1}`} sx={{ touchAction: 'none', cursor: 'grab', mt: 0.25 }}><DragIndicatorOutlined fontSize="small" /></IconButton>
      <Typography variant="body2" color="text.secondary" sx={{ width: 18, pt: 0.85, flexShrink: 0 }}>{index + 1}</Typography>
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap', mb: 0.5 }}>
          <Typography variant="subtitle2" sx={{ fontWeight: 750 }}>{scene.name || '未命名场景'}</Typography>
          {scene.note && <Typography variant="caption" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>{scene.note}</Typography>}
        </Stack>
        <Stack direction="row" spacing={0.65} useFlexGap sx={{ alignItems: 'center', flexWrap: 'wrap', minHeight: 24 }}>
          {scene.conditions.map((condition, position) => <Box key={`${condition.question}-${position}`} sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.6 }}>
            {position > 0 && <Typography variant="caption" color="text.secondary">{scene.match === 'all' ? '且' : '或'}</Typography>}
            <Chip size="small" variant="outlined" label={`${questionName(condition.question)} > ${Number.isFinite(condition.threshold) ? condition.threshold : '…'}`} sx={{ height: 24 }} />
          </Box>)}
          {scene.conditions.length > 0 && <EastOutlined sx={{ fontSize: 16, color: 'text.disabled' }} />}
          <Chip size="small" color={scene.action === 'block' ? 'error' : 'success'} label={scene.action === 'block' ? '拦截' : '记录放行'} sx={{ height: 24 }} />
        </Stack>
      </Box>
      <Button size="small" aria-label={`编辑场景：${scene.name}`} onClick={onToggle}>{expanded ? '收起' : '编辑'}</Button>
      <IconButton size="small" aria-label={`删除场景：${scene.name}`} onClick={onDelete} color="inherit"><DeleteOutlineOutlined fontSize="small" /></IconButton>
    </Stack>
    <Collapse in={expanded} unmountOnExit>
      <Box sx={{ borderTop: '1px solid', borderColor: 'divider', px: { xs: 1.5, sm: 2 }, py: 1.75 }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'minmax(180px, 1fr) minmax(220px, 2fr)' }, gap: 1.5, mb: 2 }}>
          <TextField size="small" label="场景名称" value={scene.name} onChange={event => onPatch({ name: event.target.value })} error={attempted && !scene.name.trim()} helperText={attempted && !scene.name.trim() ? '请填写场景名称' : undefined} />
          <TextField size="small" label="注释（选填）" value={scene.note || ''} onChange={event => onPatch({ note: event.target.value })} />
        </Box>
        <Stack spacing={0.85}>
          {scene.conditions.map((condition, position) => {
            const question = questions.find(item => item.key === condition.question)
            const duplicate = scene.conditions.some((item, i) => i !== position && item.question === condition.question)
            return <Stack key={position} direction="row" spacing={0.75} sx={{ alignItems: 'center', minWidth: 0 }}>
              <FormControl size="small" error={attempted && (!question || duplicate)} sx={{ minWidth: 0, flex: 1, maxWidth: 310 }}>
                <InputLabel>审核项</InputLabel>
                <Select label="审核项" value={condition.question} onChange={event => patchCondition(position, { question: event.target.value })}>
                  {questions.map(item => <MenuItem key={item.key} value={item.key} disabled={scene.conditions.some((existing, i) => i !== position && existing.question === item.key)}>{questionName(item.key)}</MenuItem>)}
                </Select>
              </FormControl>
              {question && <Tooltip title={questionMeta[question.key]?.description || ''}><IconButton size="small" aria-label={`关于${questionName(question.key)}`}><HelpOutlineOutlined fontSize="small" /></IconButton></Tooltip>}
              <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>&gt;</Typography>
              <TextField size="small" type="number" label="阈值" value={Number.isFinite(condition.threshold) ? condition.threshold : ''} onChange={event => patchCondition(position, { threshold: event.target.value === '' ? Number.NaN : Number(event.target.value) })} error={attempted && !validCondition(condition, questions)} placeholder={question ? `0–${question.max}` : ''} slotProps={{ htmlInput: { 'aria-label': `${question ? questionName(question.key) : '审核项'}阈值`, min: 0, max: question?.max, step: 0.01 } }} sx={{ width: 118, flexShrink: 0 }} />
              <IconButton size="small" aria-label={`删除条件 ${position + 1}`} onClick={() => onPatch({ conditions: scene.conditions.filter((_, i) => i !== position) })}><DeleteOutlineOutlined fontSize="small" /></IconButton>
            </Stack>
          })}
        </Stack>
        {attempted && scene.conditions.length === 0 && <Typography variant="caption" color="error.main" sx={{ display: 'block', mt: 0.75 }}>至少添加一个条件</Typography>}
        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.25} sx={{ alignItems: { sm: 'center' }, mt: 1 }}>
          <FormControl size="small" sx={{ minWidth: 170 }}><InputLabel>满足条件</InputLabel><Select label="满足条件" value={scene.match} onChange={event => onPatch({ match: event.target.value as Match })}><MenuItem value="any">任一项</MenuItem><MenuItem value="all">全部项</MenuItem></Select></FormControl>
          <Button size="small" startIcon={<AddOutlined />} onClick={() => onPatch({ conditions: [...scene.conditions, { question: '', threshold: Number.NaN }] })}>添加条件</Button>
        </Stack>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mt: 2, pt: 1.5, borderTop: '1px solid', borderColor: 'divider' }}>
          <FormControl size="small" sx={{ minWidth: 170 }}><InputLabel>处理方式</InputLabel><Select label="处理方式" value={scene.action} onChange={event => onPatch({ action: event.target.value as Action })}><MenuItem value="block">拦截</MenuItem><MenuItem value="allow">记录放行</MenuItem></Select></FormControl>
        </Box>
      </Box>
    </Collapse>
  </Paper>
}

export default function SettingsPage({ policy, upstream = { base_url: '', updated_at: '' }, onSave, onSaveUpstream = async next => ({ ...next, updated_at: '' }) }: Props) {
  const [draft, setDraft] = useState<Policy>(() => copyPolicy(policy))
  const [expanded, setExpanded] = useState<string | null>(null)
  const [attempted, setAttempted] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [upstreamURL, setUpstreamURL] = useState(upstream.base_url)
  const [upstreamError, setUpstreamError] = useState('')
  const [upstreamSaving, setUpstreamSaving] = useState(false)
  useEffect(() => { setDraft(copyPolicy(policy)); setAttempted(false); setError('') }, [policy])
  useEffect(() => { setUpstreamURL(upstream.base_url); setUpstreamError('') }, [upstream])
  const original = useMemo(() => copyPolicy(policy), [policy])
  const dirty = JSON.stringify(draft) !== JSON.stringify(original)
  const upstreamDirty = upstreamURL !== upstream.base_url
  let upstreamValid = false
  try { const parsed = new URL(upstreamURL); upstreamValid = ['http:', 'https:'].includes(parsed.protocol) && !parsed.username && !parsed.password && !parsed.search && !parsed.hash && (!parsed.pathname || parsed.pathname === '/') } catch { /* inline validation below */ }
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))

  function patchScene(index: number, patch: Partial<Scene>) {
    setDraft(current => ({ ...current, scenes: current.scenes.map((item, i) => i === index ? { ...item, ...patch } : item) }))
  }
  function dragEnd(event: DragEndEvent) {
    if (!event.over || event.active.id === event.over.id) return
    setDraft(current => {
      const from = current.scenes.findIndex(scene => scene.id === event.active.id)
      const to = current.scenes.findIndex(scene => scene.id === event.over?.id)
      return from < 0 || to < 0 ? current : { ...current, scenes: arrayMove(current.scenes, from, to) }
    })
  }
  async function save() {
    setAttempted(true); setError('')
    const bad = draft.scenes.find(scene => !validScene(scene, policy.questions))
    if (bad) { setExpanded(bad.id); return }
    if (draft.enabled && draft.scenes.length === 0) { setError('请先添加场景'); return }
    setSaving(true)
    try { await onSave(draft) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') }
    finally { setSaving(false) }
  }
  async function saveUpstream() {
    setUpstreamSaving(true); setUpstreamError('')
    if (!upstreamValid) { setUpstreamError('请输入 HTTP(S) 根地址'); setUpstreamSaving(false); return }
    try { const saved = await onSaveUpstream({ base_url: upstreamURL.trim() }); setUpstreamURL(saved.base_url) }
    catch (cause) { setUpstreamError(cause instanceof Error ? cause.message : '转发目标保存失败') }
    finally { setUpstreamSaving(false) }
  }

  return <Stack spacing={1.75}>
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' }, justifyContent: 'space-between' }}>
      <Typography variant="h5" sx={{ fontWeight: 750 }}>场景规则</Typography>
      <Stack direction="row" spacing={1.5} sx={{ alignItems: 'center' }}>
        <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}><Switch checked={draft.enabled} onChange={(_, checked) => setDraft({ ...draft, enabled: checked })} slotProps={{ input: { 'aria-label': '开启审查' } }} size="small" /><Typography variant="body2">{draft.enabled ? '审查中' : '审查关闭'}</Typography></Stack>
        <Button variant="contained" size="small" startIcon={<AddOutlined />} onClick={() => { const id = crypto.randomUUID(); setDraft(current => ({ ...current, scenes: [...current.scenes, { id, name: '', note: '', conditions: [], match: 'any', action: 'block' }] })); setExpanded(id) }}>新增场景</Button>
      </Stack>
    </Stack>
    <Paper variant="outlined" sx={{ px: 1.75, py: 1.5 }}>
      <Stack direction={{ xs: 'column', md: 'row' }} spacing={1.25} sx={{ alignItems: { md: 'center' }, justifyContent: 'space-between' }}>
        <Box sx={{ minWidth: 0 }}><Typography variant="subtitle2" sx={{ fontWeight: 750 }}>转发目标</Typography></Box>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'flex-start', flex: 1, maxWidth: { md: 720 } }}><TextField size="small" fullWidth label="转发目标地址" value={upstreamURL} onChange={event => setUpstreamURL(event.target.value)} error={Boolean(upstreamURL) && !upstreamValid} helperText={Boolean(upstreamURL) && !upstreamValid ? '请输入 HTTP(S) 根地址' : undefined} placeholder="https://api.example.com" /><Button variant="contained" size="small" disabled={!upstreamDirty || upstreamSaving || !upstreamValid} onClick={() => void saveUpstream()} sx={{ mt: 0.5, flexShrink: 0 }}>{upstreamSaving ? '保存中…' : '保存'}</Button></Stack>
      </Stack>
      {upstreamError && <Alert severity="error" sx={{ mt: 1 }}>{upstreamError}</Alert>}
    </Paper>
    {draft.scenes.length === 0 ? <Paper variant="outlined" sx={{ py: 5, textAlign: 'center' }}><Typography color="text.secondary">暂无场景</Typography></Paper> :
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={dragEnd}>
        <SortableContext items={draft.scenes.map(scene => scene.id)} strategy={verticalListSortingStrategy}>
          <Stack spacing={1}>{draft.scenes.map((scene, index) => <SceneCard key={scene.id} scene={scene} index={index} questions={policy.questions} expanded={expanded === scene.id} attempted={attempted} onToggle={() => setExpanded(expanded === scene.id ? null : scene.id)} onPatch={patch => patchScene(index, patch)} onDelete={() => setDraft(current => ({ ...current, scenes: current.scenes.filter(item => item.id !== scene.id) }))} />)}</Stack>
        </SortableContext>
      </DndContext>}
    <Paper variant="outlined" sx={{ position: 'sticky', bottom: 8, zIndex: 1, px: 1.5, py: 1, display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', boxShadow: '0 8px 24px rgba(25,37,58,.08)' }}>
      <Box sx={{ flex: 1, minWidth: 150 }}>{error ? <Alert severity="error" sx={{ py: 0 }}>{error}</Alert> : <Typography variant="body2" color="text.secondary">{dirty ? '有未保存的修改' : '已保存'}</Typography>}</Box>
      <Button size="small" disabled={!dirty || saving} onClick={() => setDraft(copyPolicy(policy))}>放弃</Button>
      <Button size="small" variant="contained" disabled={!dirty || saving} onClick={() => void save()}>{saving ? '保存中…' : '保存并生效'}</Button>
    </Paper>
  </Stack>
}
