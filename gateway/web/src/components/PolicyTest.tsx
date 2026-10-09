import { useEffect, useRef, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronDown,
  Clock3,
  FileText,
  Eraser,
  FlaskConical,
  Loader2,
  RotateCcw,
  ShieldCheck,
  ShieldX,
  Snowflake,
} from "lucide-react";
import { request } from "../api";
import { endpoints, endpointGroup, duration } from "../analytics";
import { questionMeta, questionName } from "../questionMeta";
import type { Policy } from "../policy";
import type { Answer } from "../types";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { Choice, EndpointLabel } from "./common";
import { ScoreBreakdown } from "./ScoreBreakdown";
import { notifyError } from "../notifications";
import "./newpolicy-test.css";

export type Simulation = {
  scores: Answer[];
  skipped?: boolean;
  input_chars?: number;
  input_limit_chars?: number;
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
      record_only?: boolean;
    }[];
  }[];
};
type Sample = {
  text: string;
  scores?: Answer[];
  endpoint: string;
  model?: string;
  record_id?: string;
  record_time?: string;
  return_url?: string;
};
type View = "compare" | "active" | "draft";
type Side = "active" | "draft";

function requestPolicy(policy: Policy): Policy {
  return {
    enabled: policy.enabled,
    scenes: policy.scenes,
    preview_chars: policy.preview_chars,
    retention_days: policy.retention_days,
    trusted_key_idle_days: policy.trusted_key_idle_days,
    block_message: policy.block_message,
  };
}

function requiredQuestions(
  policy: Policy,
  endpoint: string,
  model: string,
  defaultAll = false,
) {
  if (!policy.scenes.length) return defaultAll ? Object.keys(questionMeta) : [];
  const needed = new Set<string>();
  for (const scene of policy.scenes) {
    if (scene.enabled === false || scene.needs_endpoint_selection) continue;
    if (
      scene.endpoints?.length &&
      !scene.endpoints.includes(endpointGroup(endpoint))
    )
      continue;
    if (scene.models?.length && !scene.models.includes(model)) continue;
    scene.conditions.forEach((condition) => needed.add(condition.question));
  }
  return [...needed];
}
function missingQuestions(needed: string[], scores?: Answer[]) {
  const available = new Set<string>(),
    duplicate = new Set<string>();
  for (const score of scores || []) {
    if (available.has(score.question)) duplicate.add(score.question);
    const max =
      score.question === "sexual" || score.question === "gore" ? 3 : 1;
    if (Number.isFinite(score.value) && score.value >= 0 && score.value <= max)
      available.add(score.question);
  }
  return needed.filter(
    (question) => !available.has(question) || duplicate.has(question),
  );
}
function inputIdentity(text: string, endpoint: string, model: string) {
  return JSON.stringify([text, endpoint, model]);
}
function freezeSeconds(
  policy: Policy | undefined,
  result: Simulation | undefined,
) {
  if (!policy || !result?.decision.scene_id || result.skipped) return 0;
  const scene = policy.scenes.find(
    (item) => item.id === result.decision.scene_id,
  );
  return result.decision.action === "block" &&
    scene?.session_block_enabled &&
    scene.enabled !== false &&
    !scene.needs_endpoint_selection
    ? scene.session_block_ttl_seconds || 0
    : 0;
}
function completePolicy(policy: Policy) {
  const sceneIds = new Set<string>();
  return policy.scenes.every((scene) => {
    if (
      !scene.id ||
      sceneIds.has(scene.id) ||
      !scene.name.trim() ||
      !scene.conditions.length
    )
      return false;
    if (
      scene.session_block_enabled &&
      (!Number.isInteger(scene.session_block_ttl_seconds) ||
        !scene.session_block_ttl_seconds ||
        scene.session_block_ttl_seconds < 60 ||
        scene.session_block_ttl_seconds > 9223372036)
    )
      return false;
    sceneIds.add(scene.id);
    const questions = new Set<string>();
    return scene.conditions.every((condition) => {
      const max =
        condition.question === "gore" || condition.question === "sexual"
          ? 3
          : 1;
      if (
        !questionMeta[condition.question] ||
        questions.has(condition.question) ||
        !Number.isFinite(condition.threshold) ||
        condition.threshold < 0 ||
        condition.threshold > max
      )
        return false;
      questions.add(condition.question);
      return true;
    });
  });
}
const statuses: Record<string, string> = {
  effective: "生效",
  shadowed: "低优先级命中",
  priority_skipped: "前序场景已命中，未继续匹配",
  not_matched: "未命中",
  endpoint_skipped: "端点不适用",
  disabled: "已暂停",
  model_skipped: "模型不适用",
};

function ResultCard({
  side,
  result,
  missing,
  busy,
  hasScores,
  disabled,
  incomplete,
  freeze,
}: {
  side: Side;
  result?: Simulation;
  missing: string[];
  busy: boolean;
  hasScores: boolean;
  disabled?: boolean;
  incomplete?: boolean;
  freeze: number;
}) {
  const title = side === "active" ? "正在使用" : "当前草稿";
  const matches =
    result?.trace.filter(
      (scene) => scene.status === "effective" || scene.status === "shadowed",
    ) || [];
  const others =
    result?.trace.filter(
      (scene) => scene.status !== "effective" && scene.status !== "shadowed",
    ) || [];
  const comparisons =
    result?.trace
      .filter(
        (scene) =>
          scene.status !== "disabled" &&
          scene.status !== "endpoint_skipped" &&
          scene.status !== "model_skipped",
      )
      .flatMap((scene) => scene.conditions) || [];
  const action = result?.skipped
    ? "skip"
    : result?.decision.action === "block"
      ? "block"
      : result?.decision.scene_id
        ? "record"
        : "allow";
  function sceneRow(scene: Simulation["trace"][number]) {
    return (
      <div key={scene.id} className={`trial-scene-result ${scene.status}`}>
        <div className="trial-scene-name">
          <span>{scene.name}</span>
          <small>{statuses[scene.status] || scene.status}</small>
        </div>
        {scene.conditions.length > 0 && (
          <div className="trial-condition-results">
            {scene.conditions.map((condition) => (
              <span
                key={condition.question}
                className={condition.matched ? "is-match" : ""}
              >
                {questionName(condition.question)} <b>{condition.value}</b>{" "}
                <span className="trial-condition-comparator">
                  &gt; {condition.threshold}
                </span>
                {condition.matched && <Check size={11} />}
                {condition.record_only !== undefined && (
                  <span>{condition.record_only ? "仅记录" : "拒绝"}</span>
                )}
              </span>
            ))}
          </div>
        )}
      </div>
    );
  }
  return (
    <section
      className={`trial-result-card ${side}`}
      aria-label={`${title}的结果`}
    >
      <header className="trial-result-header">
        <span className={`trial-config-dot ${side}`} />
        <h3>{title}</h3>
        <span>{side === "active" ? "已保存配置" : "编辑中的配置"}</span>
      </header>
      {disabled ? (
        <div className="trial-pending">
          <ShieldCheck size={22} />
          <strong>审查已关闭</strong>
        </div>
      ) : incomplete ? (
        <div className="trial-pending">
          <FileText size={22} />
          <strong>场景配置未完成</strong>
        </div>
      ) : result ? (
        <>
          <div className={`trial-outcome ${action}`}>
            <span className="trial-outcome-icon">
              {action === "block" ? (
                <ShieldX />
              ) : action === "skip" ? (
                <ArrowRight />
              ) : action === "record" ? (
                <FileText />
              ) : (
                <ShieldCheck />
              )}
            </span>
            <div>
              <strong>
                {action === "skip"
                  ? "输入超限"
                  : action === "block"
                    ? "拦截"
                    : action === "record"
                      ? "记录放行"
                      : "放行"}
              </strong>
              <span>
                {result.decision.scene_name ||
                  (result.skipped ? "跳过审查" : "无场景命中")}
              </span>
            </div>
          </div>
          {freeze > 0 && (
            <div className="trial-freeze-action">
              <Snowflake size={13} />
              冻结后续会话 ·{" "}
              {(freeze / 60).toLocaleString("zh-CN", {
                maximumFractionDigits: 2,
              })}{" "}
              分钟
            </div>
          )}
          {result.skipped ? (
            <div className="trial-limit">
              <span>
                输入字符数
                <b>{result.input_chars?.toLocaleString()} 字符</b>
              </span>
              <span>
                送审上限
                <b>{result.input_limit_chars?.toLocaleString()} 字符</b>
              </span>
            </div>
          ) : (
            <>
              {matches.map(sceneRow)}
              {others.length > 0 && (
                <details className="trial-other-scenes">
                  <summary>
                    <ChevronDown size={13} />
                    其他场景<span>{others.length}</span>
                  </summary>
                  {others.map(sceneRow)}
                </details>
              )}
              {result.scores.length > 0 && (
                <ScoreBreakdown
                  scores={result.scores}
                  comparisons={comparisons}
                />
              )}
            </>
          )}
        </>
      ) : missing.length > 0 && hasScores ? (
        <div className="trial-pending">
          <FlaskConical size={22} />
          <strong>缺少审核项</strong>
          <div className="trial-question-tags">
            {missing.map((question) => (
              <span key={question}>{questionName(question)}</span>
            ))}
          </div>
        </div>
      ) : (
        <div className="trial-pending">
          {busy ? (
            <Loader2 size={22} className="animate-spin" />
          ) : (
            <FlaskConical size={22} />
          )}
          <strong>{busy ? "试算中…" : "等待试算"}</strong>
        </div>
      )}
    </section>
  );
}

export function PolicyTest({
  policy,
  activePolicy,
  sample,
  onScoresChange,
}: {
  policy: Policy;
  activePolicy?: Policy;
  sample?: Sample;
  onScoresChange?: (scores: Answer[] | undefined) => void;
}) {
  const [text, setText] = useState(sample?.text || ""),
    [endpoint, setEndpoint] = useState(sample?.endpoint || "openai_chat"),
    [model, setModel] = useState(sample?.model || ""),
    [scoreSample, setScoreSample] = useState(
      sample?.scores
        ? {
            scores: sample.scores,
            identity: inputIdentity(
              sample.text,
              sample.endpoint,
              sample.model || "",
            ),
          }
        : undefined,
    ),
    [results, setResults] = useState<Partial<Record<Side, Simulation>>>({}),
    [skipped, setSkipped] = useState<Simulation>(),
    [classifying, setClassifying] = useState(false),
    [evaluating, setEvaluating] = useState(false),
    [evaluationFailed, setEvaluationFailed] = useState(false),
    [retryEvaluation, setRetryEvaluation] = useState(0),
    [classifierMs, setClassifierMs] = useState(0),
    [view, setView] = useState<View>(activePolicy ? "compare" : "draft");
  const serial = useRef(0),
    evaluationSerial = useRef(0),
    classifierAbort = useRef<AbortController | null>(null),
    observer = useRef(onScoresChange);
  observer.current = onScoresChange;
  const identity = inputIdentity(text, endpoint, model),
    scores =
      scoreSample?.identity === identity ? scoreSample.scores : undefined;
  const neededDraft = requiredQuestions(policy, endpoint, model, !activePolicy),
    neededActive = activePolicy?.enabled
      ? requiredQuestions(activePolicy, endpoint, model)
      : [];
  const missingDraft = missingQuestions(neededDraft, scores),
    missingActive = missingQuestions(neededActive, scores);
  const draftJSON = JSON.stringify(requestPolicy(policy)),
    activeJSON = JSON.stringify(activePolicy && requestPolicy(activePolicy)),
    hasComparison = !!activePolicy,
    busy = classifying || evaluating;
  const draftComplete = completePolicy(policy);

  useEffect(() => {
    if (!sample) return;
    serial.current++;
    classifierAbort.current?.abort();
    setText(sample.text);
    setEndpoint(sample.endpoint);
    setModel(sample.model || "");
    setScoreSample(
      sample.scores
        ? {
            scores: sample.scores,
            identity: inputIdentity(
              sample.text,
              sample.endpoint,
              sample.model || "",
            ),
          }
        : undefined,
    );
    setResults({});
    setSkipped(undefined);
    setClassifying(false);
    setClassifierMs(0);
  }, [sample]);
  useEffect(() => {
    observer.current?.(scores);
  }, [scores]);
  useEffect(() => {
    serial.current++;
    classifierAbort.current?.abort();
    setClassifying(false);
    setSkipped(undefined);
  }, [draftJSON, activeJSON, endpoint, model, text]);
  useEffect(
    () => () => {
      serial.current++;
      evaluationSerial.current++;
      classifierAbort.current?.abort();
    },
    [],
  );

  useEffect(() => {
    const id = ++evaluationSerial.current;
    setResults({});
    setEvaluating(false);
    setEvaluationFailed(false);
    const candidates: { side: Side; policy: Policy }[] = [
      { side: "draft", policy },
    ];
    if (activePolicy?.enabled)
      candidates.unshift({ side: "active", policy: activePolicy });
    const ready = candidates.filter(
      (candidate) =>
        completePolicy(candidate.policy) &&
        scores &&
        missingQuestions(
          requiredQuestions(candidate.policy, endpoint, model, !activePolicy),
          scores,
        ).length === 0,
    );
    if (classifying || !scores || !ready.length) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setEvaluating(true);
      Promise.allSettled(
        ready.map(async (candidate) => {
          const value = await request<Simulation>("/admin/policy/test", {
            method: "POST",
            body: JSON.stringify({
              policy: requestPolicy(candidate.policy),
              endpoint,
              model,
              scores,
            }),
            signal: controller.signal,
          });
          if (id === evaluationSerial.current)
            setResults((previous) => ({
              ...previous,
              [candidate.side]: value,
            }));
        }),
      )
        .then((outcomes) => {
          if (controller.signal.aborted || id !== evaluationSerial.current)
            return;
          const failed = outcomes.find(
            (outcome) => outcome.status === "rejected",
          );
          if (failed?.status === "rejected") {
            setEvaluationFailed(true);
            notifyError(failed.reason);
          }
        })
        .finally(() => {
          if (id === evaluationSerial.current) setEvaluating(false);
        });
    }, 140);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [
    draftJSON,
    activeJSON,
    endpoint,
    model,
    scores,
    retryEvaluation,
    classifying,
  ]);

  function invalidate() {
    serial.current++;
    evaluationSerial.current++;
    classifierAbort.current?.abort();
    setScoreSample(undefined);
    setResults({});
    setSkipped(undefined);
    setClassifying(false);
    setEvaluating(false);
    setClassifierMs(0);
  }
  async function classify() {
    if (busy || !text.trim() || !draftComplete) return;
    const id = ++serial.current,
      controller = new AbortController();
    classifierAbort.current?.abort();
    classifierAbort.current = controller;
    setClassifying(true);
    setResults({});
    setSkipped(undefined);
    try {
      const value = await request<Simulation>("/admin/policy/test", {
        method: "POST",
        body: JSON.stringify({
          policy: requestPolicy(policy),
          ...(activePolicy
            ? { compare_policy: requestPolicy(activePolicy) }
            : {}),
          endpoint,
          model,
          text,
        }),
        signal: controller.signal,
      });
      if (controller.signal.aborted || id !== serial.current) return;
      setClassifierMs(value.classifier_ms);
      if (value.skipped) {
        setScoreSample(undefined);
        setSkipped(value);
      } else setScoreSample({ scores: value.scores, identity });
    } catch (error) {
      if (!controller.signal.aborted && id === serial.current)
        notifyError(error);
    } finally {
      if (id === serial.current) setClassifying(false);
    }
  }
  const shownSides: Side[] =
    !hasComparison || view === "draft"
      ? ["draft"]
      : view === "active"
        ? ["active"]
        : ["active", "draft"];
  const compareReady = results.active && results.draft;
  const sameOutcome =
    compareReady &&
    results.active?.decision.action === results.draft?.decision.action &&
    results.active?.decision.scene_id === results.draft?.decision.scene_id &&
    freezeSeconds(activePolicy, results.active) ===
      freezeSeconds(policy, results.draft);
  const missingCount = new Set([...missingDraft, ...missingActive]).size;

  return (
    <div className="policy-trial-workbench">
      <section className="trial-sample-card" aria-label="试算输入">
        <header className="trial-sample-header">
          <span className="trial-source-icon">
            <FileText size={17} />
          </span>
          <div>
            <h3>{sample?.record_id ? "历史记录样本" : "测试输入"}</h3>
            {sample?.record_id && sample.record_time && (
              <time>
                {new Date(sample.record_time).toLocaleString("zh-CN", {
                  month: "2-digit",
                  day: "2-digit",
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </time>
            )}
          </div>
          {sample?.record_id && (
            <a
              className="trial-return-link"
              href={
                sample.return_url ||
                `/events?event=${encodeURIComponent(sample.record_id)}`
              }
            >
              <ArrowLeft size={13} />
              返回原记录
            </a>
          )}
        </header>
        <div className="trial-sample-controls">
          <label>
            端点
            <Choice
              label="测试端点"
              value={endpoint}
              onChange={(value) => {
                invalidate();
                setEndpoint(value);
              }}
              options={endpoints.map((item) => ({
                value: item.id,
                label: <EndpointLabel id={item.id} />,
              }))}
            />
          </label>
          <label>
            模型
            <Input
              aria-label="测试模型"
              placeholder="请求模型名"
              value={model}
              onChange={(event) => {
                invalidate();
                setModel(event.target.value);
              }}
            />
          </label>
        </div>
        <label className="trial-text-label">
          用户输入
          <Textarea
            aria-label="测试文本"
            placeholder="输入当前用户请求的文本"
            rows={8}
            value={text}
            onChange={(event) => {
              invalidate();
              setText(event.target.value);
            }}
          />
        </label>
        <div className="trial-sample-meta">
          <span>{Array.from(text).length.toLocaleString()} 字符</span>
          {classifierMs > 0 && (
            <span>
              <Clock3 size={12} />
              Jev 耗时 {duration(classifierMs)}
            </span>
          )}
        </div>
        <div className="trial-actions">
          <Button
            size="sm"
            disabled={!text.trim() || busy || !draftComplete}
            onClick={() => void classify()}
          >
            {classifying ? (
              <Loader2 className="animate-spin" />
            ) : (
              <FlaskConical />
            )}
            {classifying ? "分类中…" : scores ? "重新分类" : "分类并试算"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            aria-label="清空输入"
            onClick={() => {
              invalidate();
              setText("");
            }}
          >
            <Eraser />
            清空输入
          </Button>
        </div>
        {scores && (
          <div className="trial-score-summary">
            <Check size={13} />
            <span>复用 {scores.length} 项分数</span>
            {missingCount > 0 && <small>待补 {missingCount} 项</small>}
          </div>
        )}
      </section>
      <section className="trial-comparison" aria-label="配置试算结果">
        <div className="trial-comparison-toolbar">
          {hasComparison ? (
            <div
              className="trial-view-switch"
              role="group"
              aria-label="结果视图"
            >
              {(
                [
                  { value: "compare", label: "配置对照" },
                  { value: "active", label: "正在使用" },
                  { value: "draft", label: "当前草稿" },
                ] as const
              ).map((item) => (
                <button
                  key={item.value}
                  type="button"
                  aria-pressed={view === item.value}
                  onClick={() => setView(item.value)}
                >
                  {item.label}
                </button>
              ))}
            </div>
          ) : (
            <h3>试算结果</h3>
          )}
          {evaluationFailed && scores && (
            <Button
              size="xs"
              variant="outline"
              disabled={busy}
              onClick={() => setRetryEvaluation((value) => value + 1)}
            >
              <RotateCcw />
              重新试算
            </Button>
          )}
          {compareReady && (
            <span
              className={`trial-comparison-status ${sameOutcome ? "same" : "changed"}`}
            >
              {sameOutcome ? <Check size={13} /> : <ArrowRight size={13} />}
              {sameOutcome ? "处理结果相同" : "处理结果不同"}
            </span>
          )}
        </div>
        <div
          className={`trial-results-grid ${shownSides.length === 1 ? "single" : ""}`}
        >
          {shownSides.map((side) => (
            <ResultCard
              key={side}
              side={side}
              result={skipped || results[side]}
              missing={side === "active" ? missingActive : missingDraft}
              busy={busy}
              hasScores={!!scores}
              disabled={side === "active" && !activePolicy?.enabled}
              freeze={freezeSeconds(
                side === "active" ? activePolicy : policy,
                skipped || results[side],
              )}
              incomplete={
                side === "draft"
                  ? !draftComplete
                  : activePolicy && !completePolicy(activePolicy)
              }
            />
          ))}
        </div>
      </section>
    </div>
  );
}
