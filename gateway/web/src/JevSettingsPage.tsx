import { useEffect, useRef, useState } from "react";
import {
  Check,
  CircleAlert,
  FlaskConical,
  LoaderCircle,
  PlugZap,
  Save,
  RotateCcw,
  Server,
} from "lucide-react";
import { toast } from "sonner";
import "./newsettings-workbench.css";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Button } from "./components/ui/button";
import { Help, Panel } from "./components/common";
import { notifyError } from "./notifications";
import { questionMeta, questionName } from "./questionMeta";
import { duration } from "./analytics";
import type { Answer, Hit } from "./types";

export type JevConfig = {
  base_url: string;
  model: string;
  api_key_set: boolean;
  timeout_ms?: number;
  max_input_chars?: number;
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
  max_input_chars: number;
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
  input_chars?: number;
  input_limit_chars?: number;
};
type Props = {
  config: JevConfig;
  runtime?: JevRuntime | null;
  onSave: (value: JevInput) => Promise<JevConfig>;
  onTest: (text: string, connection?: JevInput) => Promise<JevTestResult>;
};
type TestRun = {
  kind: "connection" | "text";
  config: Pick<JevInput, "base_url" | "model">;
  draft: boolean;
  failed: boolean;
  data?: JevTestResult;
};

export default function JevSettingsPage({
  config,
  runtime,
  onSave,
  onTest,
}: Props) {
  const [baseURL, setBaseURL] = useState(config.base_url);
  const [model, setModel] = useState(config.model);
  const [key, setKey] = useState("");
  const [timeout, setTimeout] = useState(config.timeout_ms || 5000);
  const [maxInputChars, setMaxInputChars] = useState(
    config.max_input_chars || 5000,
  );
  const [saved, setSaved] = useState(config);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<TestRun["kind"] | null>(null);
  const [text, setText] = useState("");
  const [run, setRun] = useState<TestRun | null>(null);
  const serial = useRef(0);

  function resetFields(value: JevConfig) {
    setBaseURL(value.base_url);
    setModel(value.model);
    setKey("");
    setTimeout(value.timeout_ms || 5000);
    setMaxInputChars(value.max_input_chars || 5000);
  }
  function clearTest() {
    serial.current++;
    setRun(null);
    setTesting(null);
  }
  useEffect(() => {
    setSaved(config);
    resetFields(config);
  }, [config]);
  useEffect(() => {
    clearTest();
  }, [baseURL, model, key, timeout, maxInputChars]);

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
    Number.isInteger(timeout) &&
    timeout >= 1 &&
    timeout <= 120000 &&
    Number.isInteger(maxInputChars) &&
    maxInputChars > 0;
  const dirty =
    baseURL !== saved.base_url ||
    model !== saved.model ||
    key !== "" ||
    timeout !== (saved.timeout_ms || 5000) ||
    maxInputChars !== (saved.max_input_chars || 5000);
  const input = (): JevInput => ({
    base_url: baseURL.trim(),
    model: model.trim(),
    api_key: key.trim(),
    timeout_ms: timeout,
    max_input_chars: maxInputChars,
  });

  async function save() {
    if (saving || !valid || !dirty) return;
    setSaving(true);
    try {
      const value = await onSave(input());
      setSaved(value);
      resetFields(value);
      clearTest();
      toast.success("Jev 配置已保存");
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  async function test(connectionOnly = false) {
    if (testing || !valid || (!connectionOnly && !text.trim())) return;
    const id = ++serial.current;
    const kind = connectionOnly ? "connection" : "text";
    const target = input();
    const draft = dirty;
    setTesting(kind);
    setRun(null);
    try {
      const data = await onTest(
        connectionOnly ? "这是一条连接测试。" : text.trim(),
        draft ? target : undefined,
      );
      if (id === serial.current)
        setRun({
          kind,
          config: { base_url: target.base_url, model: target.model },
          draft,
          failed: false,
          data,
        });
    } catch (error) {
      if (id === serial.current) {
        setRun({
          kind,
          config: { base_url: target.base_url, model: target.model },
          draft,
          failed: true,
        });
        notifyError(error);
      }
    } finally {
      if (id === serial.current) setTesting(null);
    }
  }
  const result = run?.data;
  const runtimeState =
    runtime?.classifier === "ok"
      ? "正常"
      : runtime?.classifier === "error"
        ? "异常"
        : "未验证";

  return (
    <div className="jev-workbench">
      <section className="jev-active-connection" aria-label="正在使用">
        <div className="jev-active-label">
          <span className="jev-active-icon">
            <Server size={17} />
          </span>
          <strong>正在使用</strong>
          <span
            className={`settings-status-pill ${runtime?.classifier === "ok" ? "is-on" : runtime?.classifier === "error" ? "is-error" : ""}`}
          >
            生产连接 · {runtimeState}
          </span>
        </div>
        <dl className="jev-active-facts">
          <div>
            <dt>接口地址</dt>
            <dd>{saved.base_url || "—"}</dd>
          </div>
          <div>
            <dt>模型</dt>
            <dd>{saved.model || "—"}</dd>
          </div>
          <div>
            <dt>密钥</dt>
            <dd>{saved.api_key_set ? "已配置" : "未配置"}</dd>
          </div>
        </dl>
      </section>

      <Panel
        title="连接配置"
        className="jev-connection-panel"
        extra={
          <span className={`settings-status-pill ${dirty ? "is-draft" : ""}`}>
            {dirty ? "未保存" : "已保存"}
          </span>
        }
      >
        <form
          className="jev-connection-form"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <div className="jev-fields">
            <label className="field jev-field-wide">
              <span>接口地址</span>
              <Input
                aria-label="接口地址"
                placeholder="https://api.example.com/v1"
                value={baseURL}
                onChange={(event) => setBaseURL(event.target.value)}
                aria-invalid={Boolean(baseURL) && !urlValid}
                disabled={saving}
              />
            </label>
            <label className="field jev-field-wide">
              <span>模型 ID</span>
              <Input
                aria-label="模型 ID"
                placeholder="jev-latest"
                value={model}
                onChange={(event) => setModel(event.target.value)}
                disabled={saving}
              />
            </label>
            <label className="field jev-field-wide" htmlFor="jev-api-key">
              <span>
                API Key{" "}
                <Help>留空保留已保存的密钥；输入新密钥后保存才会替换。</Help>
              </span>
              <Input
                id="jev-api-key"
                type="password"
                autoComplete="new-password"
                aria-label="API Key"
                placeholder={
                  saved.api_key_set ? "留空保留当前密钥" : "输入 API Key"
                }
                value={key}
                onChange={(event) => setKey(event.target.value)}
                disabled={saving}
              />
            </label>
            <label className="field">
              <span>超时（毫秒）</span>
              <Input
                type="number"
                min={1}
                max={120000}
                step={1}
                aria-label="分类器超时（毫秒）"
                value={timeout || ""}
                onChange={(event) => setTimeout(Number(event.target.value))}
                disabled={saving}
              />
            </label>
            <label className="field">
              <span>
                送审上限{" "}
                <Help>
                  按 Unicode 字符数判断。默认 5000 字；超出这个上限的文本将跳过
                  Jev 审查并直接放行，避免压缩请求等长文本撑爆 Jev 的输入上限。
                </Help>
              </span>
              <div className="jev-unit-input">
                <Input
                  type="number"
                  min={1}
                  step={1}
                  aria-label="送审上限（字符）"
                  value={maxInputChars || ""}
                  onChange={(event) =>
                    setMaxInputChars(Number(event.target.value))
                  }
                  disabled={saving}
                />
                <span>字符</span>
              </div>
            </label>
          </div>
          <div className="jev-save-actions">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={!dirty || saving}
              onClick={() => resetFields(saved)}
            >
              <RotateCcw size={14} />
              放弃修改
            </Button>
            <Button
              type="submit"
              size="sm"
              disabled={!valid || !dirty || saving}
            >
              <Save size={14} />
              {saving ? "保存中…" : "保存 Jev 配置"}
            </Button>
          </div>
        </form>
      </Panel>

      <Panel
        title="测试工作台"
        className="jev-test-panel"
        extra={
          <Help>
            使用左侧当前填写的配置测试，不保存配置，也不计入生产统计。
          </Help>
        }
      >
        <div className="jev-test-body">
          <div className="jev-test-target">
            <span>测试配置</span>
            <span className={`settings-status-pill ${dirty ? "is-draft" : ""}`}>
              {dirty ? "当前填写配置" : "已保存配置"}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={!valid || Boolean(testing) || saving}
              onClick={() => void test(true)}
            >
              {testing === "connection" ? (
                <LoaderCircle size={14} className="animate-spin" />
              ) : (
                <PlugZap size={14} />
              )}
              {testing === "connection" ? "测试中…" : "测试连接"}
            </Button>
          </div>
          <label className="field jev-test-input">
            <span>
              测试文本{" "}
              <span className="jev-input-count">
                {text.length.toLocaleString()} 字
              </span>
            </span>
            <Textarea
              aria-label="测试文本"
              placeholder="输入一段用户文本"
              rows={5}
              value={text}
              onChange={(event) => {
                setText(event.target.value);
                clearTest();
              }}
            />
          </label>
          <div className="jev-test-actions">
            <Button
              size="sm"
              variant="ghost"
              aria-label="清空测试"
              disabled={!text && !run && !testing}
              onClick={() => {
                setText("");
                clearTest();
              }}
            >
              <RotateCcw size={14} />
              清空
            </Button>
            <Button
              size="sm"
              disabled={!valid || !text.trim() || Boolean(testing) || saving}
              onClick={() => void test()}
            >
              {testing === "text" ? (
                <LoaderCircle size={14} className="animate-spin" />
              ) : (
                <FlaskConical size={14} />
              )}
              {testing === "text" ? "测试中…" : "测试 Jev 分类器"}
            </Button>
          </div>
          <section className="jev-test-result" aria-label="测试结果">
            {testing ? (
              <div className="jev-test-empty">
                <LoaderCircle size={23} className="animate-spin" />
                <span>正在测试</span>
              </div>
            ) : !run ? (
              <div className="jev-test-empty">
                <FlaskConical size={23} />
                <span>尚未测试</span>
              </div>
            ) : (
              <>
                <div className="jev-result-header">
                  <div
                    className={`jev-result-status ${run.failed || result?.skipped ? "is-warning" : ""}`}
                  >
                    {run.failed || result?.skipped ? (
                      <CircleAlert size={17} />
                    ) : (
                      <Check size={17} />
                    )}
                    <strong>
                      {run.failed
                        ? "测试未完成"
                        : result?.skipped
                          ? "输入超限，跳过审查"
                          : run.kind === "connection"
                            ? "连接正常"
                            : "分类完成"}
                    </strong>
                  </div>
                  {result && !result.skipped && (
                    <span className="jev-duration">
                      Jev 耗时 {duration(result.classifier_ms)}
                    </span>
                  )}
                </div>
                <div className="jev-result-source">
                  <span
                    className={`settings-status-pill ${run.draft ? "is-draft" : ""}`}
                  >
                    {run.draft ? "当前填写配置" : "已保存配置"}
                  </span>
                  <span>{run.config.model}</span>
                  <code>{run.config.base_url}</code>
                </div>
                {run.failed && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void test(run.kind === "connection")}
                  >
                    {run.kind === "connection"
                      ? "重新测试连接"
                      : "重新测试文本"}
                  </Button>
                )}
                {result?.skipped && (
                  <div className="jev-skipped-budget">
                    <span>
                      输入字符数{" "}
                      <strong>
                        {result.input_chars?.toLocaleString()} 字符
                      </strong>
                    </span>
                    <span>
                      送审上限{" "}
                      <strong>
                        {result.input_limit_chars?.toLocaleString()} 字符
                      </strong>
                    </span>
                  </div>
                )}
                {run.kind === "text" && result && !result.skipped && (
                  <div className="jev-score-grid">
                    {result.scores.map((score) => {
                      const graded = score.type === "score";
                      const range = graded ? 3 : 1;
                      return (
                        <div
                          className={`jev-score ${graded ? "is-graded" : ""}`}
                          key={score.question}
                        >
                          <div className="jev-score-heading">
                            <span>
                              {questionName(score.question)}
                              <Help>
                                {questionMeta[score.question]?.description ||
                                  score.question}
                              </Help>
                            </span>
                            <strong>{score.value}</strong>
                          </div>
                          <div className="jev-score-track">
                            <span
                              style={{
                                width: `${Math.max(0, Math.min(100, (score.value / range) * 100))}%`,
                              }}
                            />
                            {graded && (
                              <>
                                <i style={{ left: "33.333%" }} />
                                <i style={{ left: "66.666%" }} />
                              </>
                            )}
                          </div>
                          <div className="jev-score-scale">
                            {graded ? (
                              <>
                                <span>0</span>
                                <span>1</span>
                                <span>2</span>
                                <span>3</span>
                              </>
                            ) : (
                              <>
                                <span>0</span>
                                <span>1</span>
                              </>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      </Panel>
    </div>
  );
}
