import { useEffect, useRef, useState } from "react";
import { Check, FlaskConical, Pause, RotateCcw, X } from "lucide-react";
import { request } from "../api";
import { endpoints, duration } from "../analytics";
import { questionName } from "../questionMeta";
import type { Policy } from "../policy";
import type { Answer } from "../types";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { Choice, EndpointLabel, ErrorState } from "./common";

export type Simulation = {
  scores: Answer[];
  skipped?: boolean;
  input_tokens_estimated?: number;
  input_limit_tokens?: number;
  classifier_ms: number;
  policy_ready: boolean;
  decision: {
    action: "allow" | "block";
    scene_id?: string;
    scene_name?: string;
  };
  trace: {
    id: string;
    name: string;
    status: string;
    conditions: {
      question: string;
      value: number;
      threshold: number;
      matched: boolean;
    }[];
  }[];
};
const statuses: Record<string, string> = {
  effective: "最终生效",
  shadowed: "优先级未采用",
  not_matched: "未匹配",
  endpoint_skipped: "端点不适用",
  disabled: "已暂停",
  model_skipped: "模型不适用",
};
export function PolicyTest({
  policy,
  sample,
}: {
  policy: Policy;
  sample?: {
    text: string;
    scores?: Answer[];
    endpoint: string;
    model?: string;
  };
}) {
  const [text, setText] = useState(sample?.text || ""),
    [endpoint, setEndpoint] = useState(sample?.endpoint || "openai_chat"),
    [model, setModel] = useState(sample?.model || ""),
    [result, setResult] = useState<Simulation | null>(null),
    [scores, setScores] = useState<Answer[] | undefined>(sample?.scores),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const serial = useRef(0),
    current = useRef({ policy, endpoint, model, text });
  current.current = { policy, endpoint, model, text };
  useEffect(() => {
    if (sample) {
      setText(sample.text);
      setEndpoint(sample.endpoint);
      setModel(sample.model || "");
      setScores(sample.scores);
      setResult(null);
    }
  }, [sample]);
  const policyText = JSON.stringify(policy);
  useEffect(() => {
    if (!scores) return;
    const controller = new AbortController(),
      id = ++serial.current;
    const timer = setTimeout(() => {
      setBusy(true);
      setError("");
      request<Simulation>("/admin/policy/test", {
        method: "POST",
        body: JSON.stringify({
          policy: current.current.policy,
          endpoint: current.current.endpoint,
          model: current.current.model,
          scores,
        }),
        signal: controller.signal,
      })
        .then((value) => {
          if (id === serial.current) setResult(value);
        })
        .catch((error) => {
          if (!controller.signal.aborted && id === serial.current)
            setError(error.message);
        })
        .finally(() => {
          if (id === serial.current) setBusy(false);
        });
    }, 180);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [policyText, endpoint, model, scores]);
  async function classify() {
    const id = ++serial.current;
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const value = await request<Simulation>("/admin/policy/test", {
        method: "POST",
        body: JSON.stringify({ policy, endpoint, model, text }),
      });
      if (id === serial.current) {
        setScores(value.skipped ? undefined : value.scores);
        setResult(value);
      }
    } catch (error) {
      if (id === serial.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (id === serial.current) setBusy(false);
    }
  }
  return (
    <div className="test-grid">
      <div className="test-input">
        <Choice
          label="测试端点"
          value={endpoint}
          onChange={setEndpoint}
          options={endpoints.map((e) => ({
            value: e.id,
            label: <EndpointLabel id={e.id} />,
          }))}
        />
        <Input
          aria-label="测试模型"
          placeholder="请求模型名"
          value={model}
          onChange={(e) => setModel(e.target.value)}
        />
        <Textarea
          aria-label="测试文本"
          placeholder="输入当前用户请求的文本"
          rows={5}
          value={text}
          onChange={(e) => {
            serial.current++;
            setText(e.target.value);
            setScores(undefined);
            setResult(null);
            setBusy(false);
          }}
        />
        <div className="heading-actions">
          <Button
            size="sm"
            disabled={!text.trim() || busy}
            onClick={() => void classify()}
          >
            <FlaskConical size={14} />
            {busy ? "试算中…" : scores ? "重新分类" : "分类并试算"}
          </Button>
          {scores && (
            <span className="small muted">复用 {scores.length} 项分数</span>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="清空试算"
            onClick={() => {
              serial.current++;
              setScores(undefined);
              setText("");
              setResult(null);
              setError("");
              setBusy(false);
            }}
          >
            <RotateCcw />
          </Button>
        </div>
        {error && <ErrorState error={error} />}
      </div>
      <div className="test-result">
        {result ? (
          <>
            <div className="editor-block-title">
              <h3
                style={{
                  fontSize: 14,
                  color:
                    result.decision.action === "block" ? "#b55d6a" : "#4a8e79",
                }}
              >
                {result.skipped
                  ? "输入超限，跳过审查"
                  : result.decision.action === "block"
                    ? "拦截"
                    : result.decision.scene_id
                      ? "记录放行"
                      : "放行"}
                {result.decision.scene_name && (
                  <span
                    className="small muted"
                    style={{ fontWeight: 400, marginLeft: 8 }}
                  >
                    {result.decision.scene_name}
                  </span>
                )}
              </h3>
              {result.classifier_ms > 0 && (
                <span className="small muted">
                  {duration(result.classifier_ms)}
                </span>
              )}
            </div>
            {result.skipped && (
              <p className="small muted">
                估算 {result.input_tokens_estimated?.toLocaleString()} / 上限{" "}
                {result.input_limit_tokens?.toLocaleString()} Token
              </p>
            )}
            {result.trace.map((t) => (
              <div key={t.id} className={`trace-item ${t.status}`}>
                <div className="trace-dot">
                  {t.status === "effective" ? (
                    <Check size={11} />
                  ) : t.status === "disabled" ? (
                    <Pause size={10} />
                  ) : t.status === "not_matched" ? (
                    <X size={10} />
                  ) : (
                    <span>·</span>
                  )}
                </div>
                <div>
                  <div className="trace-label">{t.name}</div>
                  <div className="trace-conditions">
                    {t.conditions.map((c) => (
                      <span
                        key={c.question}
                        className={c.matched ? "matched" : ""}
                      >
                        {questionName(c.question)} {c.value} &gt; {c.threshold}{" "}
                        {c.matched ? "✓" : "×"}
                      </span>
                    ))}
                  </div>
                </div>
                <span className="trace-status">{statuses[t.status]}</span>
              </div>
            ))}
            <details style={{ marginTop: 15 }}>
              <summary className="small muted" style={{ cursor: "pointer" }}>
                全部原始分数
              </summary>
              <div className="score-grid" style={{ marginTop: 13 }}>
                {result.scores.map((s) => (
                  <div className="score-item" key={s.question}>
                    <div>
                      <span>{questionName(s.question)}</span>
                      <b>{s.value}</b>
                    </div>
                    <div className="score-bar">
                      <span
                        style={{
                          width: `${(s.value / (s.type === "score" ? 3 : 1)) * 100}%`,
                        }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </details>
          </>
        ) : (
          <div className="empty-state">{busy ? "试算中…" : "等待测试"}</div>
        )}
      </div>
    </div>
  );
}
