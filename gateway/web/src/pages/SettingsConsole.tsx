import { useEffect, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowUpRight, ShieldCheck } from "lucide-react";
import type { Policy, PolicyPatch, PolicyResponse } from "../policy";
import { request } from "../api";
import { notifyError, notifyRetry } from "../notifications";
import { PageHeading, Panel, Loading, Empty } from "../components/common";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Switch } from "../components/ui/switch";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "../components/ui/tabs";
import { RefreshButton } from "../components/RefreshButton";
import { TrustedKeySettings } from "../components/TrustedKeySettings";
import { SessionFreezeSettings } from "../components/SessionFreezeSettings";
import AccessSettings from "../components/AccessSettings";
import { fullDate, count } from "../analytics";

type Props = {
  policy: PolicyResponse;
  onSave: (p: PolicyPatch) => Promise<void>;
  jev: ReactNode;
};
const tabs = [
  ["review", "审查"],
  ["jev", "Jev 分类器"],
  ["access", "接入"],
  ["keys", "可信密钥"],
  ["cache", "审核缓存"],
];
export default function SettingsConsole({ policy, onSave, jev }: Props) {
  const [params, setParams] = useSearchParams();
  const tab = tabs.some(([id]) => id === params.get("tab"))
    ? params.get("tab")!
    : "review";
  return (
    <>
      <PageHeading title="设置" />
      <Tabs
        value={tab}
        onValueChange={(value) => setParams({ tab: value })}
        className="settings-console"
      >
        <TabsList className="settings-tabs">
          {tabs.map(([id, label]) => (
            <TabsTrigger key={id} value={id}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value="review">
          <ReviewSettings policy={policy} onSave={onSave} />
        </TabsContent>
        <TabsContent value="jev">{jev}</TabsContent>
        <TabsContent value="access">
          <AccessSettings />
        </TabsContent>
        <TabsContent value="keys">
          <TrustedKeySettings
            policy={policy}
            onSave={(next) =>
              onSave({
                version: next.version,
                trusted_key_idle_days: next.trusted_key_idle_days,
              })
            }
          />
          <TrustedKeys />
        </TabsContent>
        <TabsContent value="cache">
          <CacheSettings />
        </TabsContent>
      </Tabs>
    </>
  );
}
function ReviewSettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: Props["onSave"];
}) {
  const [busy, setBusy] = useState(false),
    [saving, setSaving] = useState(false),
    [days, setDays] = useState(String(policy.retention_days || 30));
  useEffect(
    () => setDays(String(policy.retention_days || 30)),
    [policy.retention_days],
  );
  async function toggle(enabled: boolean) {
    if (busy) return;
    setBusy(true);
    try {
      await onSave({ version: policy.version, enabled });
      toast.success(enabled ? "审查已开启" : "审查已关闭");
    } catch (error) {
      notifyError(error);
    } finally {
      setBusy(false);
    }
  }
  async function saveRetention() {
    const value = Number(days);
    if (!Number.isInteger(value) || value < 1 || value > 3650 || saving) return;
    setSaving(true);
    try {
      await onSave({ version: policy.version, retention_days: value });
      toast.success("记录设置已保存");
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className="settings-stack">
      <Panel title="请求审查">
        <div className="review-switch-row">
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
        </div>
      </Panel>
      <SessionFreezeSettings
        policy={policy}
        onSave={(next) =>
          onSave({
            version: next.version,
            session_block_on_blocking_review:
              next.session_block_on_blocking_review,
            session_block_on_nonblocking_review:
              next.session_block_on_nonblocking_review,
            session_block_ttl_seconds: next.session_block_ttl_seconds,
          })
        }
      />
      <Panel title="记录保留">
        <form
          className="settings-inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            void saveRetention();
          }}
        >
          <label className="field">
            <span>保留天数</span>
            <Input
              aria-label="记录保留天数"
              type="number"
              min={1}
              max={3650}
              value={days}
              onChange={(e) => setDays(e.target.value)}
            />
          </label>
          <Button
            type="submit"
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
      </Panel>
    </div>
  );
}
type Credential = {
  id: string;
  masked_key: string;
  upstream: string;
  first_seen_at: string;
  last_seen_at: string;
};
function TrustedKeys() {
  const [offset, setOffset] = useState(0);
  const result = useQuery({
    queryKey: ["trusted-keys", offset],
    queryFn: () =>
      request<{ items: Credential[]; total: number }>(
        `/admin/trusted-keys?offset=${offset}&limit=50`,
      ),
  });
  useEffect(() => {
    if (result.error) notifyRetry(result.error, () => void result.refetch());
  }, [result.error, result.refetch]);
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
      {result.isPending ? (
        <Loading />
      ) : !data?.items.length ? (
        <Empty />
      ) : (
        <>
          <div className="settings-table-wrap">
            <table className="settings-table">
              <thead>
                <tr>
                  <th>调用密钥</th>
                  <th>出站地址</th>
                  <th>首次验证</th>
                  <th>最近请求</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.items.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <code>{item.masked_key || "—"}</code>
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
                      <Button asChild variant="ghost" size="sm">
                        <Link
                          to={`/events?credential_id=${encodeURIComponent(item.id)}`}
                        >
                          记录
                          <ArrowUpRight size={13} />
                        </Link>
                      </Button>
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
              onClick={() => setOffset(Math.max(0, offset - 50))}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={offset + 50 >= data.total || result.isFetching}
              onClick={() => setOffset(offset + 50)}
            >
              下一页
            </Button>
          </div>
        </>
      )}
    </Panel>
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
    <div className="settings-stack">
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
            <div className="cache-metrics">
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
                <span>判定条目</span>
                <strong>{data ? count(data.entries) : "—"}</strong>
              </div>
              <div>
                <span>已用容量</span>
                <strong>
                  {data ? `${(data.used_bytes / mib).toFixed(1)} MiB` : "—"}
                </strong>
              </div>
            </div>
            <div className="cache-capacity">
              <div className="capacity-track">
                <span
                  style={{
                    width: `${data && data.effective_max_bytes ? Math.min(100, (data.used_bytes / data.effective_max_bytes) * 100) : 0}%`,
                  }}
                />
              </div>
              <span>
                {data?.effective_max_bytes
                  ? `容量上限 ${count(data.effective_max_bytes / mib)} MiB`
                  : "—"}
              </span>
            </div>
          </>
        )}
      </Panel>
      <Panel title="缓存配置">
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
            {saving ? "保存中…" : "保存"}
          </Button>
        </form>
      </Panel>
    </div>
  );
}
