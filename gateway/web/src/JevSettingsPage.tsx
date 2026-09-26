import { useEffect, useRef, useState } from "react";
import { Check, FlaskConical, PlugZap, Save, RotateCcw } from "lucide-react";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Button } from "./components/ui/button";
import { ErrorState, Help, PageHeading, Panel } from "./components/common";
import { questionName } from "./questionMeta";
import { duration } from "./analytics";
import type { Answer, Hit } from "./types";

export type JevConfig = {
  base_url: string;
  model: string;
  api_key_set: boolean;
  timeout_ms?: number;
  max_input_tokens?: number;
  updated_at: string;
};
export type JevRuntime = {
  classifier: string;
  last_checked_at?: string;
  last_error_kind?: string;
};
export type JevInput = {
  base_url: string;
  model: string;
  api_key: string;
  timeout_ms: number;
  max_input_tokens: number;
};
export type JevTestResult = {
  scores: Answer[];
  decision: {
    action: "allow" | "block";
    hits: Hit[];
    scene_name?: string;
    scene_priority?: number;
  } | null;
  policy_ready: boolean;
  classifier_ms: number;
  skipped?: boolean;
  input_tokens_estimated?: number;
  input_limit_tokens?: number;
};
type Props = {
  config: JevConfig;
  runtime?: JevRuntime | null;
  onSave: (value: JevInput) => Promise<JevConfig>;
  onTest: (text: string, connection?: JevInput) => Promise<JevTestResult>;
};
export default function JevSettingsPage({
  config,
  runtime,
  onSave,
  onTest,
}: Props) {
  const [baseURL, setBaseURL] = useState(config.base_url),
    [model, setModel] = useState(config.model),
    [key, setKey] = useState(""),
    [timeout, setTimeout] = useState(config.timeout_ms || 5000),
    [maxInputTokens, setMaxInputTokens] = useState(
      config.max_input_tokens || 28800,
    ),
    [saved, setSaved] = useState(config);
  const [error, setError] = useState(""),
    [testError, setTestError] = useState(""),
    [saving, setSaving] = useState(false),
    [testing, setTesting] = useState(false),
    [text, setText] = useState(""),
    [result, setResult] = useState<JevTestResult | null>(null),
    [connectionOK, setConnectionOK] = useState(false);
  const serial = useRef(0);
  useEffect(() => {
    setSaved(config);
    setBaseURL(config.base_url);
    setModel(config.model);
    setTimeout(config.timeout_ms || 5000);
    setMaxInputTokens(config.max_input_tokens || 28800);
  }, [config]);
  let urlValid = false;
  try {
    const u = new URL(baseURL);
    urlValid =
      ["https:", "http:"].includes(u.protocol) &&
      !u.username &&
      !u.password &&
      !u.search &&
      !u.hash;
  } catch {
    /* validation below */
  }
  const valid =
    urlValid &&
    Boolean(model.trim()) &&
    (saved.api_key_set || Boolean(key.trim())) &&
    timeout >= 1 &&
    timeout <= 120000 &&
    Number.isInteger(maxInputTokens) &&
    maxInputTokens > 0;
  const dirty =
    baseURL !== saved.base_url ||
    model !== saved.model ||
    key !== "" ||
    timeout !== (saved.timeout_ms || 5000) ||
    maxInputTokens !== (saved.max_input_tokens || 28800);
  const input = (): JevInput => ({
    base_url: baseURL.trim(),
    model: model.trim(),
    api_key: key.trim(),
    timeout_ms: timeout,
    max_input_tokens: maxInputTokens,
  });
  useEffect(() => {
    serial.current++;
    setConnectionOK(false);
    setResult(null);
    setTesting(false);
    setTestError("");
  }, [baseURL, model, key, timeout, maxInputTokens]);
  async function save() {
    setSaving(true);
    setError("");
    try {
      const value = await onSave(input());
      setSaved(value);
      setKey("");
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  }
  async function test(connectionOnly = false) {
    const id = ++serial.current;
    setTesting(true);
    setTestError("");
    setResult(null);
    try {
      const value = await onTest(
        connectionOnly ? "这是一条连接测试。" : text.trim(),
        input(),
      );
      if (id === serial.current) {
        setConnectionOK(!value.skipped);
        if (!connectionOnly) setResult(value);
      }
    } catch (error) {
      if (id === serial.current)
        setTestError(error instanceof Error ? error.message : String(error));
    } finally {
      if (id === serial.current) setTesting(false);
    }
  }
  return (
    <>
      <PageHeading title="设置" />
      <div className="section-tabs">
        <span className="section-tab active">Jev 分类器</span>
      </div>
      <div className="connection-layout">
        <Panel
          title="连接配置"
          extra={
            <span className="review-state">
              <i
                className={`state-dot ${runtime?.classifier === "ok" ? "on" : ""}`}
              />
              {runtime?.classifier === "ok"
                ? "分类器正常"
                : runtime?.classifier === "error"
                  ? "分类器异常"
                  : "未验证"}
            </span>
          }
        >
          <div className="panel-body">
            <div className="fields-grid">
              <label className="field" style={{ gridColumn: "1 / -1" }}>
                <span>接口地址</span>
                <Input
                  aria-label="接口地址"
                  placeholder="https://api.example.com/v1"
                  value={baseURL}
                  onChange={(e) => setBaseURL(e.target.value)}
                  aria-invalid={Boolean(baseURL) && !urlValid}
                />
                {baseURL && !urlValid && (
                  <p className="field-error">请输入有效的 HTTP(S) 地址</p>
                )}
              </label>
              <label className="field">
                <span>模型 ID</span>
                <Input
                  aria-label="模型 ID"
                  placeholder="jev-latest"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                />
              </label>
              <label className="field">
                <span>超时（毫秒）</span>
                <Input
                  type="number"
                  min={1}
                  max={120000}
                  aria-label="分类器超时（毫秒）"
                  value={timeout || ""}
                  onChange={(e) => setTimeout(Number(e.target.value))}
                />
              </label>
              <label className="field" style={{ gridColumn: "1 / -1" }}>
                <span>
                  送审上限（估算 Token）
                  <Help>
                    默认 28,800，为 Jev 32k 文本与单题预算的 90%。使用本地
                    cl100k_base 估算；超出后跳过审核、原样转发并记录警告。
                  </Help>
                </span>
                <Input
                  type="number"
                  min={1}
                  step={1}
                  aria-label="送审上限（估算 Token）"
                  value={maxInputTokens || ""}
                  onChange={(e) => setMaxInputTokens(Number(e.target.value))}
                />
              </label>
              <label
                className="field"
                htmlFor="jev-api-key"
                style={{ gridColumn: "1 / -1" }}
              >
                <span>
                  API Key{" "}
                  <Help>留空保留已保存的密钥；输入新密钥后保存才会替换。</Help>
                  {saved.api_key_set && (
                    <span
                      className="subtle-badge"
                      style={{ marginLeft: "auto" }}
                    >
                      已配置
                    </span>
                  )}
                </span>
                <Input
                  id="jev-api-key"
                  type="password"
                  autoComplete="new-password"
                  aria-label="API Key"
                  placeholder={
                    saved.api_key_set ? "保留当前密钥" : "输入 API Key"
                  }
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                />
              </label>
            </div>
            {error && (
              <div style={{ marginTop: 14 }}>
                <ErrorState error={error} />
              </div>
            )}
            <div className="form-actions">
              {connectionOK && (
                <span
                  className="small"
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 5,
                    color: "#4b967f",
                  }}
                >
                  <Check size={13} />
                  连接正常
                </span>
              )}
              <Button
                variant="outline"
                size="sm"
                disabled={!valid || testing}
                onClick={() => void test(true)}
              >
                <PlugZap size={14} />
                {testing ? "测试中…" : "测试连接"}
              </Button>
              <Button
                size="sm"
                disabled={!valid || !dirty || saving}
                onClick={() => void save()}
              >
                <Save size={14} />
                {saving ? "保存中…" : "保存 Jev 配置"}
              </Button>
            </div>
            {testError && (
              <div style={{ marginTop: 14 }}>
                <ErrorState error={testError} />
              </div>
            )}
          </div>
        </Panel>
        <Panel
          title="文本调试"
          extra={
            <Help>
              使用表单中的连接配置。测试不会保存请求，也不计入生产统计。
            </Help>
          }
        >
          <div className="panel-body">
            <Textarea
              aria-label="测试文本"
              placeholder="输入当前用户请求的文本"
              rows={6}
              value={text}
              onChange={(e) => {
                setText(e.target.value);
                setResult(null);
              }}
            />
            <div className="form-actions">
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="清空测试"
                onClick={() => {
                  serial.current++;
                  setText("");
                  setResult(null);
                  setTestError("");
                  setTesting(false);
                }}
              >
                <RotateCcw size={13} />
              </Button>
              <Button
                size="sm"
                disabled={!valid || !text.trim() || testing}
                onClick={() => void test()}
              >
                <FlaskConical size={14} />
                测试 Jev 分类器
              </Button>
            </div>
            {result && (
              <div
                style={{
                  borderTop: "1px solid var(--border)",
                  marginTop: 18,
                  paddingTop: 17,
                }}
              >
                <div className="editor-block-title">
                  <h3>{result.skipped ? "输入超限，跳过审查" : "审核结果"}</h3>
                  {!result.skipped && (
                    <span className="small muted">
                      {duration(result.classifier_ms)}
                    </span>
                  )}
                </div>
                {result.skipped && (
                  <p className="small muted">
                    估算 {result.input_tokens_estimated?.toLocaleString()} /
                    上限 {result.input_limit_tokens?.toLocaleString()} Token
                  </p>
                )}
                <div className="score-grid">
                  {result.scores.map((score) => (
                    <div className="score-item" key={score.question}>
                      <div>
                        <span>{questionName(score.question)}</span>
                        <b>{score.value}</b>
                      </div>
                      <div className="score-bar">
                        <span
                          style={{
                            width: `${(score.value / (score.type === "score" ? 3 : 1)) * 100}%`,
                          }}
                        />
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </Panel>
      </div>
    </>
  );
}
