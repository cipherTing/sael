import { useEffect, useMemo, useRef, useState } from "react";
import {
  DndContext,
  PointerSensor,
  KeyboardSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  Plus,
  GripVertical,
  Copy,
  Trash2,
  FlaskConical,
  Ban,
  FileCheck2,
  Search,
  RotateCcw,
  Loader2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "./components/ui/popover";
import { Checkbox } from "./components/ui/checkbox";
import {
  Choice,
  PageHeading,
  Panel,
  Empty,
  Help,
  SceneModeBadge,
} from "./components/common";
import { ModelScope } from "./components/ModelScope";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import { SceneTemplatePicker } from "./components/SceneTemplatePicker";
import { PolicyTest } from "./components/PolicyTest";
import { SceneAnalysis } from "./components/SceneAnalysis";
import { endpoints } from "./analytics";
import { notifyError } from "./notifications";
import { questionName } from "./questionMeta";
import { ConditionEditor } from "./components/ConditionEditor";
import "./scene-editor.css";
import {
  changeScene,
  normalizeScene,
  sceneReviewMode,
  sceneCanReject,
  type Condition,
  type Policy,
  type PolicyResponse,
  type Question,
  type Scene,
} from "./policy";
import type { Answer } from "./types";

export type UpstreamConfig = { base_url: string; updated_at: string };
type Props = {
  initialScene?: string;
  initialEndpoint?: string;
  policy: PolicyResponse;
  onSave: (p: Pick<Policy, "scenes">) => Promise<void>;
  storageKey?: string;
  sample?: {
    text: string;
    scores?: Answer[];
    endpoint: string;
    model?: string;
    record_id?: string;
    record_time?: string;
    return_url?: string;
  };
  onReload?: () => void;
  upstream?: UpstreamConfig;
  onSaveUpstream?: (input: { base_url: string }) => Promise<UpstreamConfig>;
};
const copyPolicy = (p: PolicyResponse): Policy => ({
  enabled: p.enabled,
  scenes: p.scenes.map(normalizeScene),
  preview_chars: p.preview_chars,
  retention_days: p.retention_days,
  block_message: p.block_message,
});
function sceneError(scene: Scene, questions: Question[]) {
  if (scene.needs_endpoint_selection) return "请重新选择适用端点";
  if (!scene.name.trim()) return "请填写场景名称";
  if (!scene.conditions.length) return "请添加审核条件";
  const seen = new Set<string>();
  for (const c of scene.conditions) {
    const q = questions.find((q) => q.key === c.question);
    if (!q || seen.has(c.question)) return "审核项重复或无效";
    if (!Number.isFinite(c.threshold) || c.threshold < 0 || c.threshold > q.max)
      return `${questionName(c.question)}须在 0–${q.max} 之间`;
    seen.add(c.question);
  }
  if (
    scene.session_block_enabled &&
    (!Number.isInteger(scene.session_block_ttl_seconds) ||
      !scene.session_block_ttl_seconds ||
      scene.session_block_ttl_seconds < 60 ||
      scene.session_block_ttl_seconds > 9223372036)
  )
    return "冻结时长须为正整数分钟";
  return "";
}
function SceneRow({
  scene,
  index,
  selected,
  dimmed,
  onSelect,
}: {
  scene: Scene;
  index: number;
  selected: boolean;
  dimmed: boolean;
  onSelect: () => void;
}) {
  const {
    attributes,
    listeners,
    setActivatorNodeRef,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: scene.id });
  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        zIndex: isDragging ? 2 : undefined,
        opacity: isDragging ? 0.65 : undefined,
      }}
      className={`scene-row ${selected ? "selected" : ""} ${dimmed ? "dimmed" : ""}`}
    >
      <button
        className="drag-handle"
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        aria-label={`拖动场景：${scene.name}`}
      >
        <GripVertical size={15} />
      </button>
      <button
        className="scene-row-body"
        style={{ textAlign: "left" }}
        onClick={onSelect}
      >
        <div className="scene-row-title">
          <span>{scene.name || "未命名场景"}</span>
          {scene.enabled === false && (
            <span className="subtle-badge">暂停</span>
          )}
          <span className="scene-index">
            {String(index + 1).padStart(2, "0")}
          </span>
        </div>
        {scene.note && <div className="scene-row-note">{scene.note}</div>}
        <div className="scene-row-meta">
          <div className="scene-endpoint-labels">
            {endpoints
              .filter(
                (e) =>
                  !scene.endpoints?.length || scene.endpoints.includes(e.id),
              )
              .map((e) => (
                <span className="scene-endpoint-label" key={e.id}>
                  <img src={e.icon} alt="" />
                  <span>{e.name}</span>
                </span>
              ))}
          </div>
          <SceneModeBadge mode={sceneReviewMode(scene)} />
        </div>
      </button>
    </div>
  );
}
function AddConditions({
  questions,
  selected,
  onAdd,
}: {
  questions: Question[];
  selected: string[];
  onAdd: (keys: string[]) => void;
}) {
  const [open, setOpen] = useState(false),
    [search, setSearch] = useState(""),
    [chosen, setChosen] = useState<string[]>([]);
  return (
    <Popover
      open={open}
      onOpenChange={(value) => {
        setOpen(value);
        if (value) {
          setChosen([]);
          setSearch("");
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          disabled={selected.length >= questions.length}
        >
          <Plus size={13} />
          添加条件
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-2">
        <Input
          aria-label="搜索审核项"
          placeholder="搜索审核项"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="mb-2"
        />
        <div style={{ maxHeight: 300, overflow: "auto" }}>
          {questions
            .filter((q) => questionName(q.key).includes(search))
            .map((q) => (
              <label
                key={q.key}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 9,
                  padding: "8px 6px",
                  fontSize: 12,
                  color: selected.includes(q.key) ? "#b6becc" : "#586781",
                }}
              >
                <Checkbox
                  checked={selected.includes(q.key) || chosen.includes(q.key)}
                  disabled={selected.includes(q.key)}
                  onCheckedChange={(checked) =>
                    setChosen((keys) =>
                      checked
                        ? [...keys, q.key]
                        : keys.filter((k) => k !== q.key),
                    )
                  }
                />
                {questionName(q.key)}
                <span className="small muted" style={{ marginLeft: "auto" }}>
                  0–{q.max}
                </span>
              </label>
            ))}
        </div>
        <Button
          className="mt-2 w-full"
          size="sm"
          disabled={!chosen.length}
          onClick={() => {
            onAdd(chosen);
            setOpen(false);
          }}
        >
          添加{chosen.length ? ` ${chosen.length} 项` : ""}
        </Button>
      </PopoverContent>
    </Popover>
  );
}
export default function SettingsPage({
  policy,
  onSave,
  storageKey,
  sample,
  onReload,
  initialScene,
  initialEndpoint,
}: Props) {
  const [draft, setDraft] = useState<Policy>(() => {
      if (storageKey) {
        try {
          const cached = sessionStorage.getItem(storageKey);
          if (cached) {
            const saved = JSON.parse(cached) as Pick<Policy, "scenes">;
            if (Array.isArray(saved.scenes))
              return {
                ...copyPolicy(policy),
                scenes: saved.scenes.map(normalizeScene),
              };
          }
        } catch {
          /* use server snapshot */
        }
      }
      return copyPolicy(policy);
    }),
    [baseScenes, setBaseScenes] = useState<Scene[]>(() =>
      policy.scenes.map(normalizeScene),
    ),
    [selected, setSelected] = useState(
      initialScene || policy.scenes[0]?.id || "",
    ),
    [filter, setFilter] = useState(initialEndpoint || ""),
    [search, setSearch] = useState(""),
    [attempted, setAttempted] = useState(false),
    [saving, setSaving] = useState(false),
    [tab, setTab] = useState(sample ? "test" : "config"),
    [trialScores, setTrialScores] = useState<Answer[] | undefined>(
      sample?.scores,
    );
  const dirty = JSON.stringify(draft.scenes) !== JSON.stringify(baseScenes),
    scene = draft.scenes.find((s) => s.id === selected) || draft.scenes[0];
  const lastPolicyScenes = useRef(JSON.stringify(policy.scenes));
  useEffect(() => {
    if (initialScene) setSelected(initialScene);
  }, [initialScene]);
  useEffect(() => {
    const serialized = JSON.stringify(policy.scenes);
    if (serialized === lastPolicyScenes.current) return;
    lastPolicyScenes.current = serialized;
    if (!dirty) {
      setDraft(copyPolicy(policy));
      setBaseScenes(policy.scenes.map(normalizeScene));
    }
  }, [policy, dirty]);
  useEffect(() => {
    if (!storageKey) return;
    if (dirty) sessionStorage.setItem(storageKey, JSON.stringify(draft));
    else sessionStorage.removeItem(storageKey);
  }, [draft, dirty, storageKey]);
  useEffect(() => {
    if (!dirty) return;
    const guard = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", guard);
    return () => window.removeEventListener("beforeunload", guard);
  }, [dirty]);
  useEffect(() => {
    if (sample) {
      setTab("test");
      setTrialScores(sample.scores);
    }
  }, [sample]);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const issues = useMemo(
    () =>
      new Map(draft.scenes.map((s) => [s.id, sceneError(s, policy.questions)])),
    [draft.scenes, policy.questions],
  );
  function patch(patch: Partial<Scene>) {
    if (scene)
      setDraft((p) => ({
        ...p,
        scenes: p.scenes.map((s) =>
          s.id === scene.id ? changeScene(s, patch) : s,
        ),
      }));
  }
  function condition(index: number, patchValue: Partial<Condition>) {
    patch({
      conditions: scene.conditions.map((c, i) =>
        i === index ? { ...c, ...patchValue } : c,
      ),
    });
  }
  function add() {
    const id = crypto.randomUUID();
    setDraft((p) => ({
      ...p,
      scenes: [
        ...p.scenes,
        {
          id,
          name: "",
          note: "",
          enabled: true,
          endpoints: [],
          models: [],
          conditions: [],
          match: "any",
          review_mode: "blocking",
          action: "block",
          session_block_enabled: false,
          session_block_ttl_seconds: 3600,
        },
      ],
    }));
    setSelected(id);
    setTab("config");
    setAttempted(false);
  }
  function duplicate() {
    if (!scene) return;
    const next = {
      ...structuredClone(scene),
      id: crypto.randomUUID(),
      name: `${scene.name} 副本`,
    };
    setDraft((p) => ({ ...p, scenes: [...p.scenes, next] }));
    setSelected(next.id);
    toast.success("场景已复制");
  }
  function remove() {
    if (!scene) return;
    const previous = structuredClone(scene),
      index = draft.scenes.findIndex((s) => s.id === scene.id);
    setDraft((p) => ({
      ...p,
      scenes: p.scenes.filter((s) => s.id !== scene.id),
    }));
    setSelected(draft.scenes[index === 0 ? 1 : 0]?.id || "");
    toast("场景已移除", {
      action: {
        label: "撤销",
        onClick: () => {
          setDraft((p) => {
            const scenes = [...p.scenes];
            scenes.splice(index, 0, previous);
            return { ...p, scenes };
          });
          setSelected(previous.id);
        },
      },
    });
  }
  function reorder(event: DragEndEvent) {
    if (!event.over || event.active.id === event.over.id) return;
    setDraft((p) => ({
      ...p,
      scenes: arrayMove(
        p.scenes,
        p.scenes.findIndex((s) => s.id === event.active.id),
        p.scenes.findIndex((s) => s.id === event.over!.id),
      ),
    }));
  }
  async function save() {
    if (saving) return;
    setAttempted(true);
    const invalid = draft.scenes.find((s) => issues.get(s.id));
    if (invalid) {
      setSelected(invalid.id);
      setTab("config");
      notifyError(new Error(issues.get(invalid.id)!));
      return;
    }
    if (policy.enabled && !draft.scenes.length) {
      notifyError(new Error("请先创建场景"));
      return;
    }
    setSaving(true);
    try {
      await onSave({ scenes: draft.scenes });
      setBaseScenes(structuredClone(draft.scenes));
      if (storageKey) sessionStorage.removeItem(storageKey);
      toast.success("场景已生效");
    } catch (e) {
      notifyError(e, {
        action: onReload
          ? { label: "载入线上策略", onClick: onReload }
          : undefined,
      });
    } finally {
      setSaving(false);
    }
  }
  const scoped = (id: string) => scene?.endpoints?.includes(id) || false;
  return (
    <div className="scene-console">
      <PageHeading title="场景">
        <Button size="sm" onClick={add}>
          <Plus size={14} />
          新建场景
        </Button>
      </PageHeading>
      <Tabs value={tab} onValueChange={setTab} className="scene-page-tabs-root">
        <TabsList variant="line" className="scene-page-tabs">
          <TabsTrigger value="config">场景配置</TabsTrigger>
          <TabsTrigger value="test">
            <FlaskConical size={14} />
            试算
          </TabsTrigger>
          <TabsTrigger value="analysis">场景分析</TabsTrigger>
        </TabsList>
        <TabsContent value="config">
          <div className="scene-workspace">
            <aside className="scene-list">
              <div className="scene-list-toolbar">
                <div style={{ position: "relative" }}>
                  <Search
                    size={13}
                    style={{
                      position: "absolute",
                      left: 9,
                      top: 11,
                      color: "#a3adbd",
                    }}
                  />
                  <Input
                    aria-label="搜索场景"
                    placeholder="搜索场景"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    style={{ paddingLeft: 29 }}
                  />
                </div>
                <Choice
                  label="定位端点场景"
                  visibleLabel="端点筛选"
                  value={filter}
                  onChange={setFilter}
                  options={[
                    { value: "", label: "全部端点" },
                    ...endpoints.map((e) => ({ value: e.id, label: e.name })),
                  ]}
                />
              </div>
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={reorder}
              >
                <SortableContext
                  items={draft.scenes.map((s) => s.id)}
                  strategy={verticalListSortingStrategy}
                >
                  <div className="scene-list-items">
                    {draft.scenes.length ? (
                      draft.scenes.map((s, i) => (
                        <SceneRow
                          key={s.id}
                          scene={s}
                          index={i}
                          selected={s.id === scene?.id}
                          dimmed={Boolean(
                            (filter &&
                              s.endpoints?.length &&
                              !s.endpoints.includes(filter)) ||
                            (search &&
                              !`${s.name} ${s.note || ""}`.includes(search)),
                          )}
                          onSelect={() => {
                            setSelected(s.id);
                          }}
                        />
                      ))
                    ) : (
                      <Empty text="暂无场景" />
                    )}
                  </div>
                </SortableContext>
              </DndContext>
            </aside>
            <div className="scene-editor">
              {scene ? (
                <div className="panel scene-editor-panel">
                  <div className="editor-heading">
                    <strong>{scene.name || "新建场景"}</strong>
                    <SceneTemplatePicker
                      scenes={draft.scenes.filter(
                        (s) => s.id !== scene.id && s.name.trim(),
                      )}
                      onSelect={(template) =>
                        setDraft((p) => ({
                          ...p,
                          scenes: p.scenes.map((s) =>
                            s.id === scene.id
                              ? {
                                  ...structuredClone(template),
                                  id: scene.id,
                                  name:
                                    scene.name.trim() ||
                                    `${template.name} 副本`,
                                }
                              : s,
                          ),
                        }))
                      }
                    />
                    <Switch
                      aria-label="启用此场景"
                      checked={scene.enabled !== false}
                      onCheckedChange={(enabled) => patch({ enabled })}
                    />
                    <span className="scene-enabled-label">
                      {scene.enabled === false ? "已暂停" : "已启用"}
                    </span>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label="复制场景"
                      onClick={duplicate}
                    >
                      <Copy size={14} />
                      复制场景
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label="删除场景"
                      onClick={remove}
                    >
                      <Trash2 size={14} />
                      删除场景
                    </Button>
                  </div>
                  <div className="editor-block scene-scope-block">
                    <div className="fields-grid">
                      <label className="field">
                        <span>场景名称</span>
                        <Input
                          aria-label="场景名称"
                          value={scene.name}
                          onChange={(e) => patch({ name: e.target.value })}
                          aria-invalid={attempted && !scene.name.trim()}
                        />
                      </label>
                      <label className="field">
                        <span>注释</span>
                        <Input
                          aria-label="场景注释"
                          placeholder="选填"
                          value={scene.note || ""}
                          onChange={(e) => patch({ note: e.target.value })}
                        />
                      </label>
                    </div>
                  </div>
                  <div className="editor-block">
                    <div className="editor-block-title">
                      <h3>适用端点</h3>
                    </div>
                    <div className="endpoint-options">
                      <button
                        className={`endpoint-option ${!scene.endpoints?.length ? "selected" : ""}`}
                        aria-pressed={!scene.endpoints?.length}
                        onClick={() =>
                          patch({
                            endpoints: [],
                            needs_endpoint_selection: false,
                          })
                        }
                      >
                        全部端点
                      </button>
                      {endpoints.map((e) => (
                        <button
                          key={e.id}
                          aria-label={`适用于 ${e.name}`}
                          aria-pressed={scoped(e.id)}
                          className={`endpoint-option ${scoped(e.id) ? "selected" : ""}`}
                          onClick={() =>
                            patch({
                              needs_endpoint_selection: false,
                              endpoints: scoped(e.id)
                                ? scene.endpoints!.filter((id) => id !== e.id)
                                : [...(scene.endpoints || []), e.id],
                            })
                          }
                        >
                          <img src={e.icon} alt="" />
                          {e.name}
                        </button>
                      ))}
                    </div>
                    <div className="scene-model-scope">
                      <ModelScope
                        key={scene.id}
                        models={scene.models || []}
                        suggestions={[
                          ...new Set(
                            draft.scenes.flatMap((s) => s.models || []),
                          ),
                        ]}
                        onChange={(models) => patch({ models })}
                      />
                    </div>
                  </div>
                  <div className="editor-block scene-conditions-block">
                    <div className="editor-block-title">
                      <h3>
                        匹配条件{" "}
                        <Help>
                          分数严格超过阈值才满足条件；场景从上到下匹配。
                        </Help>
                      </h3>
                      <div className="segmented">
                        <button
                          className={scene.match === "any" ? "selected" : ""}
                          aria-pressed={scene.match === "any"}
                          onClick={() => patch({ match: "any" })}
                        >
                          满足任一
                        </button>
                        <button
                          className={scene.match === "all" ? "selected" : ""}
                          aria-pressed={scene.match === "all"}
                          onClick={() => patch({ match: "all" })}
                        >
                          满足全部
                        </button>
                      </div>
                    </div>
                    <div className="conditions scene-condition-grid">
                      {scene.conditions.map((c, i) => {
                        const q = policy.questions.find(
                            (q) => q.key === c.question,
                          ),
                          invalid =
                            attempted &&
                            (!q ||
                              !Number.isFinite(c.threshold) ||
                              c.threshold < 0 ||
                              c.threshold > q.max);
                        return (
                          <ConditionEditor
                            key={i}
                            index={i}
                            showDisposition={scene.match === "any"}
                            dispositionDisabled={
                              sceneReviewMode(scene) === "non_blocking"
                            }
                            condition={c}
                            invalid={invalid}
                            questions={policy.questions.filter(
                              (q) =>
                                q.key === c.question ||
                                !scene.conditions.some(
                                  (other) => other.question === q.key,
                                ),
                            )}
                            sampleScore={
                              trialScores?.find(
                                (score) => score.question === c.question,
                              )?.value
                            }
                            onChange={(value) => condition(i, value)}
                            onRemove={() =>
                              patch({
                                conditions: scene.conditions.filter(
                                  (_, j) => j !== i,
                                ),
                              })
                            }
                          />
                        );
                      })}
                    </div>
                    <div
                      style={{ marginTop: scene.conditions.length ? 10 : 0 }}
                    >
                      <AddConditions
                        questions={policy.questions}
                        selected={scene.conditions.map((c) => c.question)}
                        onAdd={(keys) =>
                          patch({
                            conditions: [
                              ...scene.conditions,
                              ...keys.map((question) => ({
                                question,
                                threshold: NaN,
                              })),
                            ],
                          })
                        }
                      />
                    </div>
                  </div>
                  <div className="editor-block scene-treatment-block">
                    <div className="editor-block-title">
                      <h3>审查方式</h3>
                    </div>
                    <div className="action-options">
                      <button
                        className={`action-option block ${sceneReviewMode(scene) === "blocking" ? "selected" : ""}`}
                        aria-pressed={sceneReviewMode(scene) === "blocking"}
                        onClick={() => patch({ review_mode: "blocking" })}
                      >
                        <Ban size={16} />
                        阻塞性审查
                      </button>
                      <button
                        className={`action-option ${sceneReviewMode(scene) === "non_blocking" ? "selected" : ""}`}
                        aria-pressed={sceneReviewMode(scene) === "non_blocking"}
                        onClick={() => patch({ review_mode: "non_blocking" })}
                      >
                        <FileCheck2 size={16} />
                        非阻塞性审查
                      </button>
                    </div>
                    <p className="scene-treatment-help">
                      {sceneReviewMode(scene) === "blocking"
                        ? "等待审核结果后，再按命中处置决定拒绝或放行。"
                        : "先转发，后台审核仅记录，不拒绝请求或冻结会话。"}
                    </p>
                    {scene.match === "all" && (
                      <div className="scene-hit-treatment">
                        <h3>全部条件命中后</h3>
                        <div className="action-options">
                          <button
                            className={`action-option block ${scene.action === "block" ? "selected" : ""}`}
                            aria-pressed={scene.action === "block"}
                            disabled={sceneReviewMode(scene) === "non_blocking"}
                            onClick={() => patch({ action: "block" })}
                          >
                            <Ban size={16} />
                            拒绝
                          </button>
                          <button
                            className={`action-option ${scene.action === "allow" ? "selected" : ""}`}
                            aria-pressed={scene.action === "allow"}
                            onClick={() => patch({ action: "allow" })}
                          >
                            <FileCheck2 size={16} />
                            仅记录
                          </button>
                        </div>
                      </div>
                    )}
                    <div className="scene-freeze">
                      <label className="scene-freeze-toggle">
                        <Switch
                          aria-label="命中后冻结会话"
                          checked={Boolean(scene.session_block_enabled)}
                          disabled={!sceneCanReject(scene)}
                          onCheckedChange={(enabled) =>
                            patch({
                              session_block_enabled: enabled,
                              session_block_ttl_seconds:
                                scene.session_block_ttl_seconds || 3600,
                            })
                          }
                        />
                        <span>命中后冻结会话</span>
                        <Help>
                          只有实际拒绝的命中才冻结会话；仅记录不冻结。
                        </Help>
                      </label>
                      {scene.session_block_enabled && (
                        <label className="field scene-freeze-duration">
                          <span>冻结时长（分钟）</span>
                          <Input
                            aria-label="冻结时长（分钟）"
                            type="number"
                            min={1}
                            step={1}
                            value={
                              scene.session_block_ttl_seconds
                                ? scene.session_block_ttl_seconds / 60
                                : ""
                            }
                            onChange={(e) =>
                              patch({
                                session_block_ttl_seconds:
                                  e.target.value === ""
                                    ? 0
                                    : Number(e.target.value) * 60,
                              })
                            }
                          />
                        </label>
                      )}
                    </div>
                  </div>
                </div>
              ) : (
                <div className="panel">
                  <div className="empty-state">
                    <Button variant="outline" size="sm" onClick={add}>
                      <Plus />
                      新建场景
                    </Button>
                  </div>
                </div>
              )}
            </div>
          </div>
        </TabsContent>
        <TabsContent value="test" forceMount hidden={tab !== "test"}>
          <div className="scene-test-panels">
            <PolicyTest
              policy={draft}
              activePolicy={policy}
              sample={sample}
              onScoresChange={setTrialScores}
            />
          </div>
        </TabsContent>
        <TabsContent value="analysis">
          {scene ? (
            <Panel title={scene.name || "场景分析"}>
              <SceneAnalysis scene={scene} questions={policy.questions} />
            </Panel>
          ) : (
            <Empty text="暂无场景" />
          )}
        </TabsContent>
      </Tabs>
      {(dirty || saving) && (
        <div className="scene-page-save-bar dirty" role="status">
          <span className="scene-save-status">
            {saving ? (
              <Loader2 size={15} className="animate-spin" />
            ) : (
              <span className="scene-draft-dot" />
            )}
            <span>{saving ? "正在保存" : "有未保存的修改"}</span>
          </span>
          {dirty && (
            <div className="scene-save-actions">
              <Button
                variant="ghost"
                size="sm"
                disabled={saving}
                onClick={() => {
                  setDraft(copyPolicy(policy));
                  setBaseScenes(policy.scenes.map(normalizeScene));
                  setAttempted(false);
                }}
              >
                <RotateCcw size={13} />
                放弃修改
              </Button>
              <Button size="sm" disabled={saving} onClick={() => void save()}>
                {saving ? "保存中…" : "保存并生效"}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
