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
  Empty,
  Help,
  SceneModeBadge,
} from "./components/common";
import { ModelScope } from "./components/ModelScope";
import { SceneTemplatePicker } from "./components/SceneTemplatePicker";
import { PolicyTest } from "./components/PolicyTest";
import { SceneAnalysis } from "./components/SceneAnalysis";
import { endpoints } from "./analytics";
import { questionName, questionMeta } from "./questionMeta";
import {
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
  onSave: (p: Policy) => Promise<void>;
  storageKey?: string;
  sample?: {
    text: string;
    scores?: Answer[];
    endpoint: string;
    model?: string;
  };
  onReload?: () => void;
  upstream?: UpstreamConfig;
  onSaveUpstream?: (input: { base_url: string }) => Promise<UpstreamConfig>;
};
const copyPolicy = (p: PolicyResponse): Policy => ({
  enabled: p.enabled,
  version: p.version,
  scenes: structuredClone(p.scenes),
  preview_chars: p.preview_chars,
  retention_days: p.retention_days,
  session_block_enabled: Boolean(p.session_block_enabled),
  session_block_ttl_seconds: p.session_block_ttl_seconds || 3600,
});
function sceneError(scene: Scene, questions: Question[]) {
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
          <div className="endpoint-icons">
            {endpoints
              .filter(
                (e) =>
                  !scene.endpoints?.length || scene.endpoints.includes(e.id),
              )
              .map((e) => (
                <span key={e.id} title={e.name}>
                  <img src={e.icon} alt={e.name} />
                </span>
              ))}
          </div>
          <SceneModeBadge action={scene.action} />
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
          if (cached) return JSON.parse(cached) as Policy;
        } catch {
          /* use server snapshot */
        }
      }
      return copyPolicy(policy);
    }),
    [selected, setSelected] = useState(
      initialScene || policy.scenes[0]?.id || "",
    ),
    [filter, setFilter] = useState(initialEndpoint || ""),
    [search, setSearch] = useState(""),
    [attempted, setAttempted] = useState(false),
    [error, setError] = useState(""),
    [saving, setSaving] = useState(false),
    [inspector, setInspector] = useState<"test" | "analysis" | null>(
      sample ? "test" : null,
    );
  const dirty = JSON.stringify(draft) !== JSON.stringify(copyPolicy(policy)),
    scene = draft.scenes.find((s) => s.id === selected) || draft.scenes[0];
  const previousVersion = useRef(policy.version);
  const inspectorRef = useRef<HTMLDivElement>(null);
  const revealInspector = (value: "test" | "analysis") => {
    setInspector(value);
    requestAnimationFrame(() =>
      inspectorRef.current?.scrollIntoView({
        behavior: "smooth",
        block: "start",
      }),
    );
  };
  useEffect(() => {
    if (initialScene) setSelected(initialScene);
  }, [initialScene]);
  useEffect(() => {
    if (previousVersion.current !== policy.version) {
      previousVersion.current = policy.version;
      setDraft(copyPolicy(policy));
      setError("");
    }
  }, [policy]);
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
    if (sample) setInspector("test");
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
          s.id === scene.id ? { ...s, ...patch } : s,
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
          match: "all",
          action: "block",
        },
      ],
    }));
    setSelected(id);
    setAttempted(false);
    setError("");
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
    setAttempted(true);
    const invalid = draft.scenes.find((s) => issues.get(s.id));
    if (invalid) {
      setSelected(invalid.id);
      setError(issues.get(invalid.id)!);
      return;
    }
    if (draft.enabled && !draft.scenes.length) {
      setError("请先创建场景");
      return;
    }
    setSaving(true);
    setError("");
    try {
      await onSave(draft);
      if (storageKey) sessionStorage.removeItem(storageKey);
      toast.success("场景已生效");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }
  const scoped = (id: string) => scene?.endpoints?.includes(id) || false;
  return (
    <>
      <PageHeading title="场景">
        <div className="review-state">
          <Switch
            aria-label="开启审查"
            checked={draft.enabled}
            onCheckedChange={(enabled) => setDraft((p) => ({ ...p, enabled }))}
          />
          <span>审查{draft.enabled ? "开启" : "关闭"}</span>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => revealInspector("test")}
        >
          <FlaskConical size={14} />
          试算
        </Button>
        <Button size="sm" onClick={add}>
          <Plus size={14} />
          新建场景
        </Button>
      </PageHeading>
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
                        setError("");
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
            <div className="panel">
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
                                scene.name.trim() || `${template.name} 副本`,
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
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="复制场景"
                  onClick={duplicate}
                >
                  <Copy size={14} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="删除场景"
                  onClick={remove}
                >
                  <Trash2 size={14} />
                </Button>
              </div>
              <div className="editor-block">
                <div className="fields-grid">
                  <label className="field">
                    <span>场景名称</span>
                    <Input
                      aria-label="场景名称"
                      value={scene.name}
                      onChange={(e) => patch({ name: e.target.value })}
                      aria-invalid={attempted && !scene.name.trim()}
                    />
                    {attempted && !scene.name.trim() && (
                      <p className="field-error">请填写场景名称</p>
                    )}
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
                    onClick={() => patch({ endpoints: [] })}
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
              </div>
              <div className="editor-block">
                <ModelScope
                  key={scene.id}
                  models={scene.models || []}
                  suggestions={[
                    ...new Set(draft.scenes.flatMap((s) => s.models || [])),
                  ]}
                  onChange={(models) => patch({ models })}
                />
              </div>
              <div className="editor-block">
                <div className="editor-block-title">
                  <h3>
                    匹配条件{" "}
                    <Help>
                      每项分数严格超过所填阈值才满足条件。场景从上到下执行，首个匹配场景决定处理动作。
                    </Help>
                  </h3>
                  <div className="segmented">
                    <button
                      className={scene.match === "all" ? "selected" : ""}
                      onClick={() => patch({ match: "all" })}
                    >
                      满足全部
                    </button>
                    <button
                      className={scene.match === "any" ? "selected" : ""}
                      onClick={() => patch({ match: "any" })}
                    >
                      满足任一
                    </button>
                  </div>
                </div>
                <div className="conditions">
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
                      <div key={i} className="condition-row">
                        <Choice
                          label={`审核项 ${i + 1}`}
                          value={c.question}
                          onChange={(question) => condition(i, { question })}
                          options={policy.questions
                            .filter(
                              (q) =>
                                q.key === c.question ||
                                !scene.conditions.some(
                                  (c) => c.question === q.key,
                                ),
                            )
                            .map((q) => ({
                              value: q.key,
                              label: questionName(q.key),
                            }))}
                        />
                        <Help>{questionMeta[c.question]?.description}</Help>
                        <span className="operator">&gt;</span>
                        <Input
                          aria-label={`${questionName(c.question)}阈值`}
                          className={invalid ? "condition-error" : ""}
                          type="number"
                          min="0"
                          max={q?.max}
                          step="0.01"
                          value={
                            Number.isFinite(c.threshold) ? c.threshold : ""
                          }
                          onChange={(e) =>
                            condition(i, {
                              threshold:
                                e.target.value === ""
                                  ? NaN
                                  : Number(e.target.value),
                            })
                          }
                        />
                        <span className="range">0–{q?.max}</span>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          aria-label={`删除条件 ${i + 1}`}
                          onClick={() =>
                            patch({
                              conditions: scene.conditions.filter(
                                (_, j) => j !== i,
                              ),
                            })
                          }
                        >
                          <Trash2 size={12} />
                        </Button>
                      </div>
                    );
                  })}
                </div>
                <div style={{ marginTop: scene.conditions.length ? 10 : 0 }}>
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
                {attempted && issues.get(scene.id) && (
                  <p className="field-error">{issues.get(scene.id)}</p>
                )}
              </div>
              <div className="editor-block">
                <div className="editor-block-title">
                  <h3>审查方式</h3>
                </div>
                <div className="action-options">
                  <button
                    className={`action-option block ${scene.action === "block" ? "selected" : ""}`}
                    onClick={() => patch({ action: "block" })}
                  >
                    <Ban size={16} />
                    阻塞性审查
                  </button>
                  <button
                    className={`action-option ${scene.action === "allow" ? "selected" : ""}`}
                    onClick={() => patch({ action: "allow" })}
                  >
                    <FileCheck2 size={16} />
                    非阻塞性审查
                  </button>
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
          {(dirty || error) && (
            <div className="save-bar">
              <span>
                {error ? (
                  <span role="alert" style={{ color: "#bf5665" }}>
                    {error}
                  </span>
                ) : (
                  "有未保存的修改"
                )}
              </span>
              {error && onReload && (
                <Button size="sm" variant="ghost" onClick={onReload}>
                  载入线上策略
                </Button>
              )}
              <Button
                variant="ghost"
                size="sm"
                disabled={saving}
                onClick={() => {
                  setDraft(copyPolicy(policy));
                  setError("");
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
          <div className="test-workspace" ref={inspectorRef}>
            <div className="section-tabs">
              <button
                className={`section-tab ${inspector === "test" ? "active" : ""}`}
                onClick={() =>
                  setInspector((value) => (value === "test" ? null : "test"))
                }
              >
                草稿试算
              </button>
              <button
                className={`section-tab ${inspector === "analysis" ? "active" : ""}`}
                onClick={() =>
                  setInspector((value) =>
                    value === "analysis" ? null : "analysis",
                  )
                }
              >
                场景分析
              </button>
            </div>
            {inspector === "test" && (
              <div className="panel">
                <PolicyTest policy={draft} sample={sample} />
              </div>
            )}
            {inspector === "analysis" && scene && (
              <div className="panel">
                <SceneAnalysis scene={scene} questions={policy.questions} />
              </div>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
