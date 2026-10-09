import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound, ShieldCheck } from "lucide-react";
import { request } from "../api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import { Switch } from "../components/ui/switch";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../components/ui/tabs";
import { Panel, Empty, Loading, CopyButton } from "../components/common";
import { RefreshButton } from "../components/RefreshButton";
import { fullDate, count } from "../analytics";
import { notifyError, notifyRetry } from "../notifications";
import type { PolicyResponse, PolicyPatch } from "../policy";
import "../review-api.css";

type Props = {
  policy: PolicyResponse;
  onSave: (patch: PolicyPatch) => Promise<void>;
};
type Endpoint = { id: string; name: string; path: string };
type Access = { ingress_url: string; endpoints: Endpoint[] };
type Key = {
  id: string;
  name: string;
  note?: string;
  masked: string;
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
  request_count?: number;
  secret?: string;
};
export default function ReviewAPIPage({ policy, onSave }: Props) {
  const [params, setParams] = useSearchParams();
  const [saving, setSaving] = useState(false);
  const selected = params.get("review_api_tab") || "docs";
  const tab = ["docs", "keys"].includes(selected) ? selected : "docs";
  const enabled = Boolean(policy.review_api_enabled);

  async function toggle(value: boolean) {
    if (saving) return;
    setSaving(true);
    try {
      await onSave({ review_api_enabled: value });
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className="review-api-page">
      <section
        className={`review-api-enable ${enabled ? "is-enabled" : ""}`}
        aria-label="HTTP 审核接口开关"
      >
        <div className="review-api-enable-icon">
          <ShieldCheck size={21} />
        </div>
        <div className="review-api-enable-copy">
          <div className="review-api-enable-title">
            <h2>HTTP 审核接口</h2>
            <span className={`review-api-status ${enabled ? "on" : ""}`}>
              <i />
              {enabled ? "接口已启用" : "接口未启用"}
            </span>
          </div>
          <p>
            为外部应用提供同步文本审核。场景规则、Jev
            分类器和输入字符上限共用现有设置；此开关只控制本接口。
          </p>
        </div>
        <Switch
          aria-label="HTTP 审核接口"
          checked={enabled}
          disabled={saving}
          onCheckedChange={(value) => void toggle(value)}
        />
      </section>
      <Tabs
        value={tab}
        onValueChange={(value) => {
          const next = new URLSearchParams(params);
          next.set("review_api_tab", value);
          setParams(next);
        }}
        className="review-api-layout"
      >
        <div className="review-api-navigation">
          <TabsList className="review-api-tabs" aria-label="审核接口设置">
            <TabsTrigger value="docs">接入说明</TabsTrigger>
            <TabsTrigger value="keys">密钥管理</TabsTrigger>
          </TabsList>
        </div>
        <div className="review-api-content">
          <TabsContent value="docs">
            <DocsTab />
          </TabsContent>
          <TabsContent value="keys">
            <KeysTab />
          </TabsContent>
        </div>
      </Tabs>
    </div>
  );
}

function CodeExample({ title, value }: { title?: string; value: string }) {
  return (
    <div className="review-api-code-example">
      {title && <strong>{title}</strong>}
      <CopyButton value={value} label={`复制${title || "示例代码"}`} />
      <pre>
        <code>{value}</code>
      </pre>
    </div>
  );
}

function DocsTab() {
  const access = useQuery({
    queryKey: ["review-api-access"],
    queryFn: () => request<Access>("/admin/access"),
  });
  useEffect(() => {
    if (access.error) notifyRetry(access.error, () => void access.refetch());
  }, [access.error, access.refetch]);
  const base = (access.data?.ingress_url || "<Sael 进网地址>").replace(
    /\/$/,
    "",
  );
  const url = `${base}/v1/moderations`;
  const endpoints = access.data?.endpoints || [];
  const curl = `curl -X POST '${url}' \\
  -H 'Authorization: Bearer <审核密钥>' \\
  -H 'Content-Type: application/json' \\
  -d '{"input":"请审核这段文本","endpoint":"openai_responses"}'`;
  const response = `{
  "id": "sael_mod_01J...",
  "object": "sael.moderation",
  "created": 1728345600,
  "flagged": false,
  "action": "allow",
  "scenes": [],
  "scores": [
    { "question": "cyber_abuse", "value": 0.02 }
  ]
}`;
  const blockedResponse = `{
  "id": "sael_mod_01K...",
  "object": "sael.moderation",
  "created": 1728345601,
  "flagged": true,
  "action": "block",
  "scenes": [
    {
      "id": "dangerous-content",
      "name": "危险内容",
      "matched": true,
      "hits": [
        { "question": "cyber_abuse", "value": 0.93, "threshold": 0.7 }
      ]
    }
  ],
  "scores": [
    { "question": "cyber_abuse", "value": 0.93 }
  ]
}`;
  const cachedResponse = `{
  "id": "sael_mod_01L...",
  "object": "sael.moderation",
  "created": 1728345602,
  "flagged": true,
  "action": "allow",
  "scenes": [{
    "id": "observe-content",
    "name": "关注内容",
    "matched": true,
    "hits": [{ "question": "cyber_abuse", "threshold": 0.7, "record_only": true }]
  }],
  "scores": []
}`;
  return (
    <div className="review-api-docs">
      <Panel title="审核接口接入说明">
        <div className="review-api-doc-body">
          <p>
            对外提供 <code>POST</code> 文本审核接口。每次请求同步返回 Sael
            的审核结论，不会把请求转发给模型上游。
          </p>
          <dl className="review-api-facts">
            <div>
              <dt>请求地址</dt>
              <dd>
                <code>{url}</code>
                <CopyButton value={url} label="复制审核接口地址" />
              </dd>
            </div>
            <div>
              <dt>请求方法</dt>
              <dd>
                <code>POST</code>
              </dd>
            </div>
            <div>
              <dt>请求格式</dt>
              <dd>
                <code>Content-Type: application/json</code>
              </dd>
            </div>
            <div>
              <dt>鉴权方式</dt>
              <dd>
                <code>Authorization: Bearer &lt;审核密钥&gt;</code>
              </dd>
            </div>
          </dl>
          <h3>请求参数</h3>
          <div className="review-api-table-wrap">
            <table className="review-api-param-table">
              <thead>
                <tr>
                  <th>字段</th>
                  <th>类型</th>
                  <th>必填</th>
                  <th>说明</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>
                    <code>input</code>
                  </td>
                  <td>string</td>
                  <td>是</td>
                  <td>
                    要审核的非空文本。默认最多 5000 字符；超限会跳过 Jev
                    并按未命中放行。
                  </td>
                </tr>
                <tr>
                  <td>
                    <code>endpoint</code>
                  </td>
                  <td>string</td>
                  <td>否</td>
                  <td>场景端点筛选值；省略时不按端点筛选。取值如下表。</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="review-api-doc-note">
            请求体只支持以上两个字段；传入其他字段（包括 <code>model</code>
            ）会返回 <code>400</code>。
          </p>
          <h3>endpoint 可用值</h3>
          {access.isPending ? (
            <Loading />
          ) : access.isError ? (
            <Empty text="端点列表暂时不可用，请刷新重试" />
          ) : (
            <div className="review-api-endpoints">
              {endpoints.map((endpoint) => (
                <div key={endpoint.id}>
                  <code>{endpoint.id}</code>
                  <span>{endpoint.name}</span>
                  <code>{endpoint.path}</code>
                </div>
              ))}
            </div>
          )}
          <p className="review-api-doc-note">
            endpoint 只用于选择现有场景，不改变审核规则。场景、Jev
            地址与密钥、字符上限都在原有设置中统一维护。
          </p>
          <CodeExample title="cURL 请求示例" value={curl} />
          <h3>返回示例</h3>
          <CodeExample title="安全内容示例" value={response} />
          <CodeExample title="命中并拦截示例" value={blockedResponse} />
          <CodeExample title="缓存命中且仅记录示例" value={cachedResponse} />
          <p className="review-api-doc-note">
            flagged 表示首个场景是否命中；action 为 block 时由接入方拒绝，为
            allow 时放行。仅记录命中时 flagged 仍为
            true。场景按优先级首命中即停止，scenes
            最多一项。缓存仅保存条件判定，不保存分数；缓存命中时 scores
            为空，hits 省略 value，record_only
            表示该命中条件的处置。审核接口本身统一返回 200
            的有效审核结果，action=block 不代表接口返回 HTTP 403。
          </p>
          <h3>常见错误</h3>
          <div className="review-api-error-list">
            <p>
              <code>400</code> 请求 JSON 不合法、input 为空或 endpoint 无效。
            </p>
            <p>
              <code>401</code> 缺少或无效的审核密钥。
            </p>
            <p>
              <code>404</code> HTTP 审核接口尚未启用。
            </p>
            <p>
              <code>413</code> 请求体超过服务器限制。
            </p>
            <p>
              <code>429</code> 并发请求过多，请稍后重试。
            </p>
            <p>
              <code>503</code> 配置、密钥存储或 Jev 审核暂时不可用。
            </p>
          </div>
        </div>
      </Panel>
    </div>
  );
}

function KeysTab() {
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [revoking, setRevoking] = useState("");
  const keys = useQuery({
    queryKey: ["review-api-keys"],
    queryFn: () => request<Key[]>("/admin/review-api/keys"),
  });
  useEffect(() => {
    if (keys.error) notifyRetry(keys.error, () => void keys.refetch());
  }, [keys.error, keys.refetch]);
  async function create() {
    if (busy) return;
    setBusy(true);
    try {
      const key = await request<Key>("/admin/review-api/keys", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), note: note.trim() }),
      });
      setSecret(key.secret || "");
      setName("");
      setNote("");
      await client.invalidateQueries({ queryKey: ["review-api-keys"] });
    } catch (error) {
      notifyError(error);
    } finally {
      setBusy(false);
    }
  }
  async function revoke(id: string) {
    if (revoking) return;
    setRevoking(id);
    try {
      await request(`/admin/review-api/keys/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
      await client.invalidateQueries({ queryKey: ["review-api-keys"] });
    } catch (error) {
      notifyError(error);
    } finally {
      setRevoking("");
    }
  }
  return (
    <div className="review-api-keys">
      <Panel title="创建审核 API Key">
        <div className="review-api-key-form">
          <label>
            密钥名称
            <Input
              aria-label="密钥名称"
              maxLength={80}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="例如：内容服务"
            />
          </label>
          <label>
            备注
            <Textarea
              maxLength={240}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder="可选"
            />
          </label>
          <Button
            disabled={!name.trim() || busy || Boolean(secret)}
            onClick={() => void create()}
          >
            <KeyRound size={14} />
            {busy ? "创建中…" : "创建密钥"}
          </Button>
        </div>
        {secret && (
          <div className="review-api-secret">
            <b>完整密钥只显示这一次</b>
            <code>{secret}</code>
            <div>
              <CopyButton value={secret} label="复制新密钥" />
              <Button variant="ghost" onClick={() => setSecret("")}>
                已保存密钥
              </Button>
            </div>
          </div>
        )}
      </Panel>
      <Panel
        title="已创建的密钥"
        extra={
          <RefreshButton
            label="刷新密钥"
            busy={keys.isFetching}
            onRefresh={() => keys.refetch()}
          />
        }
      >
        {keys.isPending ? (
          <Loading />
        ) : keys.isError ? (
          <Empty text="密钥列表暂时不可用，请刷新重试" />
        ) : keys.data?.length ? (
          keys.data.map((key) => (
            <div className="review-api-key-row" key={key.id}>
              <div>
                <b>
                  {key.name}
                  {key.revoked_at && <small> · 已撤销</small>}
                </b>
                <span>
                  <code>{key.masked}</code>
                  <CopyButton
                    value={key.masked}
                    label={`复制 ${key.name} 的掩码`}
                  />
                </span>
                {key.note && <p>{key.note}</p>}
                <small>
                  创建 {fullDate(key.created_at)} · 最近使用{" "}
                  {key.last_used_at ? fullDate(key.last_used_at) : "尚未使用"} ·
                  请求 {count(key.request_count || 0)} 次
                </small>
              </div>
              {!key.revoked_at && (
                <Button
                  variant="ghost"
                  disabled={Boolean(revoking)}
                  aria-label={`撤销 ${key.name}`}
                  onClick={() => void revoke(key.id)}
                >
                  <Ban size={14} />
                  {revoking === key.id ? "撤销中…" : "撤销"}
                </Button>
              )}
            </div>
          ))
        ) : (
          <Empty />
        )}
      </Panel>
    </div>
  );
}
