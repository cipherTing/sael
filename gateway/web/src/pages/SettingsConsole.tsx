import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ArrowUpRight,
  Check,
  Database,
  KeyRound,
  Network,
  PlugZap,
  Settings2,
  ShieldCheck,
  X,
} from "lucide-react";
import "../newsettings-workbench.css";
import type { Policy, PolicyPatch, PolicyResponse } from "../policy";
import { request } from "../api";
import { notifyError, notifyRetry } from "../notifications";
import { Panel, Loading, Empty, CopyButton } from "../components/common";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Slider } from "../components/ui/slider";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../components/ui/tooltip";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "../components/ui/popover";
import { Textarea } from "../components/ui/textarea";
import { Switch } from "../components/ui/switch";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "../components/ui/tabs";
import { RefreshButton } from "../components/RefreshButton";
import { TrustedKeySettings } from "../components/TrustedKeySettings";
import AccessSettings from "../components/AccessSettings";
import { fullDate, count } from "../analytics";

type Props = {
  policy: PolicyResponse;
  onSave: (p: PolicyPatch) => Promise<void>;
  jev: ReactNode;
  reviewAPI?: ReactNode;
};
const tabs = [
  { id: "gateway", label: "网关", icon: Network },
  { id: "jev", label: "Jev 分类器", icon: PlugZap },
  { id: "review-api", label: "审核接口", icon: ShieldCheck },
  { id: "keys", label: "可信密钥", icon: KeyRound },
  { id: "data", label: "数据与缓存", icon: Database },
];
export default function SettingsConsole({
  policy,
  onSave,
  jev,
  reviewAPI = null,
}: Props) {
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab") || "gateway";
  const resolved =
    (
      { review: "gateway", access: "gateway", cache: "data" } as Record<
        string,
        string
      >
    )[requested] || requested;
  const tab = tabs.some(({ id }) => id === resolved) ? resolved : "gateway";
  const tabList = useRef<HTMLDivElement>(null);
  const [visited, setVisited] = useState([tab]);
  useEffect(() => {
    tabList.current
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [tab]);
  useEffect(() => {
    setVisited((current) =>
      current.includes(tab) ? current : [...current, tab],
    );
  }, [tab]);
  return (
    <>
      <Tabs
        value={tab}
        onValueChange={(value) => setParams({ tab: value })}
        className="settings-console"
      >
        <div className="settings-navigation">
          <TabsList
            ref={tabList}
            className="settings-tabs"
            aria-label="设置分类"
          >
            {tabs.map(({ id, label, icon: Icon }) => (
              <TabsTrigger key={id} value={id}>
                <Icon size={14} />
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <TabsContent
          value="gateway"
          forceMount={visited.includes("gateway") ? true : undefined}
          hidden={tab !== "gateway"}
        >
          <GatewaySettings policy={policy} onSave={onSave} />
        </TabsContent>
        <TabsContent
          value="jev"
          forceMount={visited.includes("jev") ? true : undefined}
          hidden={tab !== "jev"}
        >
          {jev}
        </TabsContent>
        <TabsContent
          value="review-api"
          forceMount={visited.includes("review-api") ? true : undefined}
          hidden={tab !== "review-api"}
        >
          {reviewAPI}
        </TabsContent>
        <TabsContent
          value="keys"
          forceMount={visited.includes("keys") ? true : undefined}
          hidden={tab !== "keys"}
        >
          <TrustedKeys policy={policy} onSave={onSave} />
        </TabsContent>
        <TabsContent
          value="data"
          forceMount={visited.includes("data") ? true : undefined}
          hidden={tab !== "data"}
        >
          <div className="settings-stack data-settings">
            <RecordRetentionSettings policy={policy} onSave={onSave} />
            <CacheSettings />
          </div>
        </TabsContent>
      </Tabs>
    </>
  );
}
function GatewaySettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: Props["onSave"];
}) {
  const [busy, setBusy] = useState(false);
  async function toggle(enabled: boolean) {
    if (busy) return;
    setBusy(true);
    try {
      await onSave({ enabled });
      toast.success(enabled ? "审查已开启" : "审查已关闭");
    } catch (error) {
      notifyError(error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="settings-stack gateway-settings">
      <section className="review-switch-row" aria-label="全局审查设置">
        <div className="review-switch-label">
          <ShieldCheck size={22} />
          <div>
            <strong>全局审查</strong>
            <div className={policy.enabled ? "status-on" : "muted"}>
              {policy.enabled ? "已开启" : "已关闭"}
            </div>
          </div>
        </div>
        <Switch
          aria-label="全局审查"
          checked={policy.enabled}
          disabled={busy}
          onCheckedChange={(enabled) => void toggle(enabled)}
        />
      </section>
      <AccessSettings />
      <BlockMessageSettings policy={policy} onSave={onSave} />
    </div>
  );
}

function RecordRetentionSettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: Props["onSave"];
}) {
  const [saving, setSaving] = useState(false);
  const [days, setDays] = useState(String(policy.retention_days || 30));
  useEffect(
    () => setDays(String(policy.retention_days || 30)),
    [policy.retention_days],
  );
  async function saveRetention() {
    const value = Number(days);
    if (!Number.isInteger(value) || value < 1 || value > 3650 || saving) return;
    setSaving(true);
    try {
      await onSave({ retention_days: value });
      toast.success("记录设置已保存");
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  return (
    <section className="record-retention-row" aria-label="审核记录保留">
      <h2>审核记录</h2>
      <form
        className="retention-form"
        onSubmit={(e) => {
          e.preventDefault();
          void saveRetention();
        }}
      >
        <label className="retention-field">
          <span>记录保留</span>
          <Input
            aria-label="记录保留天数"
            type="number"
            min={1}
            max={3650}
            value={days}
            onChange={(e) => setDays(e.target.value)}
          />
          <span>天</span>
        </label>
        <Button
          type="submit"
          size="sm"
          aria-label="保存记录保留"
          disabled={
            saving ||
            Number(days) === (policy.retention_days || 30) ||
            !Number.isInteger(Number(days)) ||
            Number(days) < 1 ||
            Number(days) > 3650
          }
        >
          {saving ? "保存中…" : "保存"}
        </Button>
      </form>
    </section>
  );
}

const defaultBlockMessage = "Request denied.";
const blockMessageVariables = [
  { value: "{request_id}", label: "请求 ID", example: "req-preview" },
  { value: "{endpoint}", label: "请求路径", example: "/v1/responses" },
  { value: "{model}", label: "模型", example: "gpt-4.1" },
] as const;

function BlockMessageSettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: Props["onSave"];
}) {
  const saved = policy.block_message?.trim()
    ? policy.block_message
    : defaultBlockMessage;
  const [message, setMessage] = useState(saved);
  const [saving, setSaving] = useState(false);
  const editor = useRef<HTMLTextAreaElement>(null);
  useEffect(() => setMessage(saved), [saved]);
  const unknown = [...message.matchAll(/\{[^{}]*\}/g)]
    .map(([token]) => token)
    .find(
      (token) => !blockMessageVariables.some((item) => item.value === token),
    );
  const error = !message.trim()
    ? "请输入拦截提示"
    : message.length > 500
      ? "最多 500 字"
      : unknown
        ? `不支持占位符 ${unknown}`
        : /[{}]/.test(message.replace(/\{[^{}]*\}/g, ""))
          ? "占位符不完整"
          : "";
  const preview = message.replace(
    /\{request_id\}|\{endpoint\}|\{model\}/g,
    (token) =>
      blockMessageVariables.find((item) => item.value === token)?.example ||
      token,
  );

  function insert(value: string) {
    const start = editor.current?.selectionStart ?? message.length;
    const end = editor.current?.selectionEnd ?? message.length;
    setMessage(message.slice(0, start) + value + message.slice(end));
    requestAnimationFrame(() => {
      editor.current?.focus();
      editor.current?.setSelectionRange(
        start + value.length,
        start + value.length,
      );
    });
  }

  async function save() {
    if (saving || error || message === saved) return;
    setSaving(true);
    try {
      await onSave({ block_message: message });
      toast.success("拦截提示已保存");
    } catch (err) {
      notifyError(err);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel title="拦截提示" className="block-message-panel">
      <div className="block-message-settings">
        <div className="block-message-editor">
          <label className="field">
            <span>发送给客户端的错误信息</span>
            <Textarea
              ref={editor}
              aria-label="拦截提示"
              rows={3}
              maxLength={500}
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              aria-invalid={Boolean(error)}
            />
          </label>
          <div className="block-message-variables">
            {blockMessageVariables.map((item) => (
              <button
                key={item.value}
                type="button"
                aria-label={`插入${item.label}`}
                onClick={() => insert(item.value)}
              >
                <code>{item.value}</code>
                <span>{item.label}</span>
              </button>
            ))}
          </div>
          <p className="small muted">
            点击变量插入光标处，发送 403 时会替换成当前请求的值。
          </p>
          {error && <p className="small block-message-error">{error}</p>}
          <div className="heading-actions">
            <Button
              variant="outline"
              size="sm"
              disabled={saving || message === defaultBlockMessage}
              onClick={() => setMessage(defaultBlockMessage)}
            >
              恢复默认
            </Button>
            <Button
              size="sm"
              disabled={saving || Boolean(error) || message === saved}
              onClick={() => void save()}
            >
              {saving ? "保存中…" : "保存拦截提示"}
            </Button>
          </div>
        </div>
        <div className="block-message-preview">
          <span className="small muted">预览</span>
          <div className="block-message-preview-body">
            <span>403</span>
            <p>{preview || "—"}</p>
          </div>
        </div>
      </div>
    </Panel>
  );
}
type Credential = {
  id: string;
  masked_key: string;
  upstream: string;
  first_seen_at: string;
  last_seen_at: string;
  sample_rate: number;
  note: string;
};
type CredentialPage = { items: Credential[]; total: number };
function TrustedKeys({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: Props["onSave"];
}) {
  const [keyPrefix, setKeyPrefix] = useState("");
  const [noteFilter, setNoteFilter] = useState("");
  const [filter, setFilter] = useState({ keyPrefix: "", note: "", offset: 0 });
  const { offset } = filter;
  const result = useQuery({
    queryKey: ["trusted-keys", filter],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({
        offset: String(offset),
        limit: "50",
      });
      if (filter.keyPrefix) params.set("key_prefix", filter.keyPrefix);
      if (filter.note) params.set("note", filter.note);
      return request<CredentialPage>(`/admin/trusted-keys?${params}`, {
        signal,
      });
    },
  });
  useEffect(() => {
    if (result.error) notifyRetry(result.error, () => void result.refetch());
  }, [result.error, result.refetch]);
  useEffect(() => {
    const total = result.data?.total;
    if (total !== undefined && offset > 0 && offset >= total)
      setFilter((current) => ({
        ...current,
        offset: Math.max(0, Math.ceil(total / 50) - 1) * 50,
      }));
  }, [offset, result.data]);
  function applyFilters(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next = {
      keyPrefix: keyPrefix.trim(),
      note: noteFilter.trim(),
      offset: 0,
    };
    if (
      filter.keyPrefix === next.keyPrefix &&
      filter.note === next.note &&
      offset === 0
    )
      void result.refetch();
    else setFilter(next);
  }
  const data = result.data;
  return (
    <Panel
      title={`可信密钥${data ? ` · ${count(data.total)}` : ""}`}
      extra={
        <RefreshButton
          busy={result.isFetching}
          onRefresh={() => result.refetch()}
          label="刷新可信密钥"
        />
      }
    >
      <TrustedKeySettings policy={policy} onSave={onSave} />
      <form
        className="trusted-key-filters"
        aria-label="筛选可信密钥"
        onSubmit={applyFilters}
      >
        <Input
          aria-label="密钥前缀"
          placeholder="密钥前缀"
          value={keyPrefix}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setKeyPrefix(event.target.value)}
        />
        <Input
          aria-label="筛选备注"
          placeholder="备注"
          value={noteFilter}
          onChange={(event) => setNoteFilter(event.target.value)}
        />
        <Button type="submit" size="sm">
          筛选
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setKeyPrefix("");
            setNoteFilter("");
            setFilter({ keyPrefix: "", note: "", offset: 0 });
          }}
        >
          清空
        </Button>
      </form>
      {result.isPending ? (
        <Loading />
      ) : !data?.items.length ? (
        <Empty
          text={
            result.isError
              ? "可信密钥暂不可用，请刷新重试"
              : filter.keyPrefix || filter.note
                ? "没有匹配的可信密钥"
                : "暂无数据"
          }
        />
      ) : (
        <>
          <div className="settings-table-wrap">
            <table className="settings-table">
              <colgroup>
                <col />
                <col style={{ width: 180 }} />
                <col />
                <col />
                <col />
                <col style={{ width: 230 }} />
              </colgroup>
              <thead>
                <tr>
                  <th>调用密钥</th>
                  <th>备注</th>
                  <th>出站地址</th>
                  <th>首次验证</th>
                  <th>最近请求</th>
                  <th>取样率</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <div className="copyable-value">
                        <code className="record-key" title={item.masked_key}>
                          {item.masked_key || "—"}
                        </code>
                        {item.masked_key && (
                          <CopyButton
                            value={item.masked_key}
                            label="复制调用密钥"
                          />
                        )}
                      </div>
                    </td>
                    <td className="trusted-key-note-cell">
                      <TrustedKeyNoteEditor credential={item} />
                    </td>
                    <td className="credential-upstream" title={item.upstream}>
                      {item.upstream || "—"}
                    </td>
                    <td>
                      {item.first_seen_at &&
                      !item.first_seen_at.startsWith("0001")
                        ? fullDate(item.first_seen_at)
                        : "—"}
                    </td>
                    <td>{fullDate(item.last_seen_at)}</td>
                    <td>
                      <div className="trusted-key-actions">
                        <TrustedKeySamplingControl credential={item} />
                        <Button asChild variant="ghost" size="sm">
                          <Link
                            to={`/events?credential_id=${encodeURIComponent(item.id)}`}
                          >
                            查看记录
                            <ArrowUpRight size={13} />
                          </Link>
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="settings-pagination">
            <span>
              {offset + 1}–{offset + data.items.length} / {data.total}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={offset === 0 || result.isFetching}
              onClick={() =>
                setFilter((current) => ({
                  ...current,
                  offset: Math.max(0, offset - 50),
                }))
              }
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={offset + 50 >= data.total || result.isFetching}
              onClick={() =>
                setFilter((current) => ({ ...current, offset: offset + 50 }))
              }
            >
              下一页
            </Button>
          </div>
        </>
      )}
    </Panel>
  );
}

function TrustedKeyNoteEditor({ credential }: { credential: Credential }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(credential.note || "");
  const [saving, setSaving] = useState(false);
  const note = draft.trim();
  const invalid =
    Array.from(note).length > 240 || /[\r\n\u0085\u2028\u2029]/.test(draft);
  async function save() {
    if (invalid || saving) return;
    setSaving(true);
    try {
      const saved = await request<{ note: string }>(
        `/admin/trusted-keys/${encodeURIComponent(credential.id)}`,
        { method: "PATCH", body: JSON.stringify({ note }) },
      );
      client.setQueriesData<CredentialPage>(
        { queryKey: ["trusted-keys"] },
        (current) =>
          current && {
            ...current,
            items: current.items.map((item) =>
              item.id === credential.id ? { ...item, note: saved.note } : item,
            ),
          },
      );
      setEditing(false);
      await client.invalidateQueries({ queryKey: ["trusted-keys"] });
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  if (!editing) {
    const noteButton = (
      <button
        type="button"
        className={`trusted-key-note ${credential.note ? "" : "trusted-key-note-empty"}`}
        aria-label={credential.note ? "编辑备注" : "添加备注"}
        onClick={() => {
          setDraft(credential.note || "");
          setEditing(true);
        }}
      >
        {credential.note || "添加备注"}
      </button>
    );
    if (!credential.note) return noteButton;
    return (
      <TooltipProvider delayDuration={0}>
        <Tooltip>
          <TooltipTrigger asChild>{noteButton}</TooltipTrigger>
          <TooltipContent
            side="top"
            sideOffset={6}
            className="max-w-80 whitespace-normal break-words leading-relaxed"
          >
            {credential.note}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }
  return (
    <form
      className="trusted-key-note-editor"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <Input
        autoFocus
        aria-label="备注内容"
        aria-invalid={invalid || undefined}
        title={invalid ? "备注须为单行，最多 240 个字符" : undefined}
        value={draft}
        disabled={saving}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          if (event.key === "Enter") {
            event.preventDefault();
            void save();
          }
          if (event.key === "Escape" && !saving) {
            event.preventDefault();
            setEditing(false);
          }
        }}
      />
      <Button
        type="submit"
        variant="ghost"
        size="icon-xs"
        aria-label="保存备注"
        title="保存"
        disabled={invalid || saving}
      >
        <Check size={14} />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label="取消编辑备注"
        title="取消"
        disabled={saving}
        onClick={() => setEditing(false)}
      >
        <X size={14} />
      </Button>
    </form>
  );
}

function TrustedKeySamplingControl({ credential }: { credential: Credential }) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [sampleRate, setSampleRate] = useState(String(credential.sample_rate));
  const [saving, setSaving] = useState(false);
  const rate = Number(sampleRate);
  const validRate =
    sampleRate !== "" && Number.isInteger(rate) && rate >= 0 && rate <= 100;

  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!validRate || saving) return;
    setSaving(true);
    try {
      const result = await request<{ sample_rate: number }>(
        `/admin/key-sampling/${encodeURIComponent(credential.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({ sample_rate: rate }),
        },
      );
      client.setQueriesData<{ items: Credential[]; total: number }>(
        { queryKey: ["trusted-keys"] },
        (current) =>
          current && {
            ...current,
            items: current.items.map((item) =>
              item.id === credential.id
                ? { ...item, sample_rate: result.sample_rate }
                : item,
            ),
          },
      );
      setOpen(false);
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="trusted-key-sampling">
      {credential.sample_rate === 0 ? (
        <span className="trusted-key-exempt">免审</span>
      ) : credential.sample_rate === 100 ? (
        <span className="trusted-key-review-all">逐一审核</span>
      ) : credential.sample_rate < 100 ? (
        <span className="trusted-key-rate">
          取样率 {credential.sample_rate}%
        </span>
      ) : null}
      <Popover
        open={open}
        onOpenChange={(next) => {
          if (next) setSampleRate(String(credential.sample_rate));
          if (!saving) setOpen(next);
        }}
      >
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="设置取样率"
            title="设置取样率"
          >
            <Settings2 size={15} />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="end"
          side="bottom"
          aria-label="设置取样率"
          className="trusted-key-sampling-popover"
        >
          <form className="trusted-key-sampling-form" onSubmit={save}>
            <div className="trusted-key-sampling-controls">
              <Slider
                min={0}
                max={100}
                step={1}
                value={[validRate ? rate : 0]}
                onValueChange={([value]) => setSampleRate(String(value))}
                thumbLabel="取样率"
                aria-label="取样率滑块"
                className="trusted-key-sampling-slider"
              />
              <label className="trusted-key-sampling-field">
                <span>取样率</span>
                <span className="trusted-key-sampling-input">
                  <Input
                    aria-label="取样率百分比"
                    type="number"
                    min={0}
                    max={100}
                    step={1}
                    value={sampleRate}
                    onChange={(event) => setSampleRate(event.target.value)}
                    required
                  />
                  <span aria-hidden="true">%</span>
                </span>
              </label>
            </div>
            <Button type="submit" size="sm" disabled={!validRate || saving}>
              {saving ? "保存中…" : "保存"}
            </Button>
          </form>
        </PopoverContent>
      </Popover>
    </div>
  );
}
type CacheState = {
  ttl_days: number;
  max_bytes: number;
  available: boolean;
  used_bytes: number;
  effective_max_bytes: number;
  entries: number;
  lookups: number;
  hits: number;
};
const mib = 1024 * 1024;
function CacheSettings() {
  const result = useQuery({
    queryKey: ["review-cache"],
    queryFn: () => request<CacheState>("/admin/review-cache"),
  });
  const [days, setDays] = useState("7"),
    [capacity, setCapacity] = useState("1024"),
    [saving, setSaving] = useState(false);
  useEffect(() => {
    if (result.data) {
      setDays(String(result.data.ttl_days));
      setCapacity(String(result.data.max_bytes / mib));
    }
  }, [result.data]);
  useEffect(() => {
    if (result.error) notifyRetry(result.error, () => void result.refetch());
  }, [result.error, result.refetch]);
  const data = result.data;
  const usage = data?.effective_max_bytes
    ? Math.max(
        0,
        Math.min(100, (data.used_bytes / data.effective_max_bytes) * 100),
      )
    : 0;
  const valid =
    Number.isInteger(Number(days)) &&
    Number(days) >= 1 &&
    Number(days) <= 3650 &&
    Number.isInteger(Number(capacity)) &&
    Number(capacity) >= 1 &&
    Number(capacity) <= 1048576;
  async function save() {
    if (!valid || saving) return;
    setSaving(true);
    try {
      await request("/admin/review-cache", {
        method: "PUT",
        body: JSON.stringify({
          ttl_days: Number(days),
          max_bytes: Number(capacity) * mib,
        }),
      });
      await result.refetch();
      toast.success("审核缓存配置已保存");
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className="cache-settings-grid">
      <Panel
        title="审核缓存"
        extra={
          <div className="heading-actions">
            <span className={data?.available ? "status-on" : "muted"}>
              {data?.available ? "已连接" : "未连接"}
            </span>
            <RefreshButton
              label="刷新缓存状态"
              busy={result.isFetching || saving}
              onRefresh={() => result.refetch()}
            />
          </div>
        }
      >
        {result.isPending ? (
          <Loading />
        ) : (
          <>
            <section className="cache-metrics" aria-label="缓存复用统计">
              <div>
                <span>复用次数</span>
                <strong>{data ? count(data.hits) : "—"}</strong>
              </div>
              <div>
                <span>复用率</span>
                <strong>
                  {data && data.lookups
                    ? `${((data.hits / data.lookups) * 100).toFixed(1)}%`
                    : "—"}
                </strong>
              </div>
              <div>
                <span>缓存条目</span>
                <strong>{data ? count(data.entries) : "—"}</strong>
              </div>
            </section>
            <div className="cache-capacity">
              <div className="cache-capacity-heading">
                <span>容量使用</span>
                <strong>
                  {data?.effective_max_bytes ? `${usage.toFixed(1)}%` : "—"}
                </strong>
              </div>
              <div
                className="capacity-track"
                role="meter"
                aria-label="缓存容量使用"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={usage}
              >
                <span style={{ width: `${usage}%` }} />
              </div>
              <div className="cache-capacity-values">
                <span>
                  {data?.effective_max_bytes
                    ? `${(data.used_bytes / mib).toFixed(1)} / ${count(data.effective_max_bytes / mib)} MiB`
                    : "—"}
                </span>
                <span>实际容量</span>
              </div>
            </div>
          </>
        )}
      </Panel>
      <Panel title="缓存配置" className="cache-configuration">
        <form
          className="settings-inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label className="field">
            <span>有效期（天）</span>
            <Input
              aria-label="缓存有效期（天）"
              type="number"
              min={1}
              max={3650}
              value={days}
              onChange={(e) => setDays(e.target.value)}
            />
          </label>
          <label className="field">
            <span>容量上限（MiB）</span>
            <Input
              aria-label="缓存容量上限（MiB）"
              type="number"
              min={1}
              max={1048576}
              value={capacity}
              onChange={(e) => setCapacity(e.target.value)}
            />
          </label>
          <Button
            type="submit"
            disabled={
              !data ||
              !valid ||
              saving ||
              (Number(days) === data.ttl_days &&
                Number(capacity) * mib === data.max_bytes)
            }
          >
            {saving ? "保存中…" : "保存缓存配置"}
          </Button>
        </form>
      </Panel>
    </div>
  );
}
